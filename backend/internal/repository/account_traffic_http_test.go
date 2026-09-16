package repository

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountTrafficHTTPEnforcesBudgetBeforeNetwork(t *testing.T) {
	_, cache, plan := trafficFixture(t)
	plan.Policy.RPM = 1
	plan.Policy.Burst = 1
	control := service.NewAccountTrafficService(cache)
	upstream := NewControlledHTTPUpstream(nil, control)
	a := &service.Account{ID: 42, Concurrency: 8, Extra: map[string]any{service.AccountTrafficPolicyKey: plan.Policy}}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: hello\n\n")
	}))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL, strings.NewReader("{}"))
	resp, err := upstream.Do(service.WithAccountTrafficRequest(req, a), "", 42, 8)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	req, _ = http.NewRequest("POST", server.URL, strings.NewReader("{}"))
	_, err = upstream.Do(service.WithAccountTrafficRequest(req, a), "", 42, 8)
	var limited *service.AccountTrafficLimitError
	require.True(t, errors.As(err, &limited))
	require.EqualValues(t, 1, calls.Load())
	// Turning both switches off immediately restores the normal transport.
	a.Extra = nil
	req, _ = http.NewRequest("POST", server.URL, strings.NewReader("{}"))
	resp, err = upstream.Do(service.WithAccountTrafficRequest(req, a), "", 42, 8)
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 2, calls.Load())
}
