package service

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNativeProxyPoolAllocatorSelectsHighestUnusedSlot(t *testing.T) {
	pool := config.GatewayNativeProxyPoolConfig{
		Enabled:        true,
		Protocol:       "http",
		Host:           "172.18.0.1",
		Port:           31289,
		UsernamePrefix: "native",
	}
	proxies := []ProxyWithAccountCount{
		{Proxy: Proxy{ID: 101, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10001"}},
		{Proxy: Proxy{ID: 102, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10003"}},
		{Proxy: Proxy{ID: 103, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10002"}, AccountCount: 1},
		{Proxy: Proxy{ID: 104, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10004"}},
		{Proxy: Proxy{ID: 105, Protocol: "https", Host: "172.18.0.1", Port: 31289, Username: "native10005"}},
		{Proxy: Proxy{ID: 106, Protocol: "http", Host: "172.18.0.2", Port: 31289, Username: "native10006"}},
		{Proxy: Proxy{ID: 107, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native-not-a-slot"}},
	}

	allocator, err := newNativeProxyPoolAllocator(proxies, pool)
	require.NoError(t, err)

	first, err := allocator.acquire()
	require.NoError(t, err)
	require.Equal(t, int64(104), *first)

	second, err := allocator.acquire()
	require.NoError(t, err)
	require.Equal(t, int64(102), *second)

	third, err := allocator.acquire()
	require.NoError(t, err)
	require.Equal(t, int64(101), *third)

	_, err = allocator.acquire()
	require.Error(t, err)
	require.True(t, errors.Is(err, errNativeProxyPoolExhausted))
}

func TestNativeProxyPoolAllocatorRejectsIncompleteConfig(t *testing.T) {
	_, err := newNativeProxyPoolAllocator(nil, config.GatewayNativeProxyPoolConfig{Enabled: true})
	require.ErrorContains(t, err, "requires protocol, host, port, and username prefix")
}

func TestParseNativeProxySlot(t *testing.T) {
	tests := []struct {
		name     string
		username string
		prefix   string
		want     int64
		ok       bool
	}{
		{name: "valid", username: "native22829", prefix: "native", want: 22829, ok: true},
		{name: "wrong prefix", username: "slot22829", prefix: "native", ok: false},
		{name: "non numeric", username: "native22x29", prefix: "native", ok: false},
		{name: "missing suffix", username: "native", prefix: "native", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseNativeProxySlot(tt.username, tt.prefix)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestProxyForCRSCreatePreservesExistingProxy(t *testing.T) {
	current := int64(77)
	existing := &Account{ID: 1, ProxyID: &current}
	allocator, err := newNativeProxyPoolAllocator([]ProxyWithAccountCount{
		{Proxy: Proxy{ID: 88, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native88"}},
	}, config.GatewayNativeProxyPoolConfig{
		Protocol:       "http",
		Host:           "172.18.0.1",
		Port:           31289,
		UsernamePrefix: "native",
	})
	require.NoError(t, err)

	got, err := proxyForCRSCreate(existing, &current, allocator)
	require.NoError(t, err)
	require.Same(t, &current, got)
}

func TestProxyForCRSCreateAllocatesPoolProxy(t *testing.T) {
	current := int64(77)
	allocator, err := newNativeProxyPoolAllocator([]ProxyWithAccountCount{
		{Proxy: Proxy{ID: 88, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native88"}},
	}, config.GatewayNativeProxyPoolConfig{
		Protocol:       "http",
		Host:           "172.18.0.1",
		Port:           31289,
		UsernamePrefix: "native",
	})
	require.NoError(t, err)

	got, err := proxyForCRSCreate(nil, &current, allocator)
	require.NoError(t, err)
	require.Equal(t, int64(88), *got)
}
