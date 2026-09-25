package mihomo

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCountryFilterRules(t *testing.T) {
	for _, tc := range []struct {
		mode, code    string
		unknown, want bool
	}{
		{"off", "", false, true}, {"exclude", "HK", false, false}, {"exclude", "US", false, true}, {"exclude", "", false, false}, {"exclude", "", true, true},
		{"include", "HK", false, true}, {"include", "US", false, false}, {"include", "", false, false}, {"include", "", true, true},
	} {
		t.Run(tc.mode+tc.code, func(t *testing.T) {
			s := saved{CountryFilter: CountryFilter{Mode: tc.mode, Codes: []string{"HK"}, AllowUnknown: tc.unknown}, Countries: map[string]CountryObservation{"node": {Code: tc.code}}}
			require.Equal(t, tc.want, countryAllowed(s, "node"))
		})
	}
	f, err := normalizeCountryFilter(CountryFilter{Mode: "exclude", Codes: []string{" hk ", "US", "HK"}})
	require.NoError(t, err)
	require.Equal(t, []string{"HK", "US"}, f.Codes)
	for _, f := range []CountryFilter{{Mode: "invalid"}, {Mode: "include"}, {Mode: "exclude", Codes: []string{"ZZ"}}, {Mode: "exclude", Codes: []string{"UK"}}, {Mode: "include", Codes: []string{"http://example.org"}}} {
		_, err := normalizeCountryFilter(f)
		require.Error(t, err)
	}
	require.Contains(t, countryCodes(), "HK")
	require.Contains(t, countryCodes(), "US")
}

func TestDynamicCountryPolicyIgnoresStaleMeasurements(t *testing.T) {
	s := saved{CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}}, Nodes: []map[string]any{{"name": "DYNAMIC-one"}, {"name": "airport-hk"}}, Countries: map[string]CountryObservation{"DYNAMIC-one": {Code: "US", CheckedAt: time.Now()}, "airport-hk": {Code: "HK"}}}
	require.False(t, countryAllowed(s, "DYNAMIC-one"), "a previously allowed IP must not qualify a new dynamic connection")
	s.CountryFilter.DynamicProviderManaged = true
	require.True(t, countryAllowed(s, "DYNAMIC-one"))
	require.False(t, countryAllowed(s, "airport-hk"), "dynamic policy must not bypass airport rules")
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.saved = s
	status := m.Status()
	require.True(t, status.NodeStates[0].Dynamic)
	require.Empty(t, status.NodeStates[0].CountryCode)
	require.Nil(t, status.NodeStates[0].CountryCheckedAt)
	b, err := m.config(s)
	require.NoError(t, err)
	var cfg struct {
		Groups []struct {
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	require.NoError(t, json.Unmarshal(b, &cfg))
	require.Contains(t, cfg.Groups[0].Proxies, "DYNAMIC-one")
	require.NotContains(t, cfg.Groups[0].Proxies, "airport-hk")
	s.Disabled = map[string]string{"DYNAMIC-one": "disabled"}
	m.saved = s
	require.Zero(t, m.Status().EligibleNodes)
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	var restored saved
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.True(t, restored.CountryFilter.DynamicProviderManaged)
	_, err = m.scanCountries(context.Background(), s, "DYNAMIC-one")
	require.ErrorContains(t, err, "dynamic exit")
}

func TestCountryFilterPersistsAndCannotBeBypassedByRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Installed = true
	m.state.Running = true
	m.state.Supported = true
	m.saved = saved{UseOnce: true, Secret: "test", Nodes: []map[string]any{{"name": "node-hk", "type": "http", "server": "127.0.0.1", "port": 1}}, Countries: map[string]CountryObservation{"node-hk": {Code: "HK", CheckedAt: time.Now()}}, Disabled: map[string]string{"node-hk": "used"}}
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	filter := CountryFilter{Mode: "exclude", Codes: []string{"HK"}}
	require.NoError(t, m.Submit("country_filter", nil, false, &filter))
	require.Eventually(t, func() bool { return !m.Status().Busy }, time.Second, 10*time.Millisecond)
	require.Empty(t, m.Status().Error)
	require.NoError(t, m.run(context.Background(), "recover/node-hk", m.saved))
	require.Equal(t, "country_excluded", m.Status().NodeStates[0].State)
	require.Zero(t, m.Status().EligibleNodes)
	require.Equal(t, 1, m.Status().CountryExcluded)
	_, err := Lease(context.Background(), Endpoint)
	require.Error(t, err)
	config, err := m.config(m.saved)
	require.NoError(t, err)
	var decoded struct {
		Groups []struct {
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	require.NoError(t, json.Unmarshal(config, &decoded))
	require.Equal(t, []string{"REJECT"}, decoded.Groups[0].Proxies)
	b, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	var persisted saved
	require.NoError(t, json.Unmarshal(b, &persisted))
	require.False(t, countryAllowed(persisted, "node-hk"))
	persisted.CountryFilter = CountryFilter{Mode: "off"}
	require.True(t, countryAllowed(persisted, "node-hk"))
}

func TestCountryFilterDoesNotChangeManualNodeState(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	m.saved = saved{Nodes: []map[string]any{{"name": "node"}}, Disabled: map[string]string{"node": "disabled"}, Countries: map[string]CountryObservation{"node": {Code: "US"}}, CountryFilter: CountryFilter{Mode: "include", Codes: []string{"US"}}}
	require.Zero(t, m.Status().EligibleNodes)
	require.Equal(t, "disabled", m.Status().NodeStates[0].State)
}
