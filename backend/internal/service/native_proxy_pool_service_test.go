package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type nativeProxyPoolProxyRepoStub struct {
	ProxyRepository
	proxies []ProxyWithAccountCount
}

func (s *nativeProxyPoolProxyRepoStub) ListActiveWithAccountCount(context.Context) ([]ProxyWithAccountCount, error) {
	return s.proxies, nil
}

type nativeProxyPoolAccountRepoStub struct {
	AccountRepository
	accounts  []Account
	updated   []Account
	updateErr error
}

func (s *nativeProxyPoolAccountRepoStub) ListSchedulableByPlatform(context.Context, string) ([]Account, error) {
	return append([]Account(nil), s.accounts...), nil
}

func (s *nativeProxyPoolAccountRepoStub) Update(_ context.Context, account *Account) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updated = append(s.updated, *account)
	return nil
}

func enabledNativeProxyPoolConfig() *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{NativeProxyPool: config.GatewayNativeProxyPoolConfig{
		Enabled:        true,
		Protocol:       "http",
		Host:           "172.18.0.1",
		Port:           31289,
		UsernamePrefix: "native",
	}}}
}

func TestNativeProxyPoolServiceCreateAccountAssignsIdleProxy(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	proxyRepo := &nativeProxyPoolProxyRepoStub{proxies: []ProxyWithAccountCount{
		{Proxy: Proxy{ID: 101, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10001"}},
		{Proxy: Proxy{ID: 102, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10002"}},
	}}
	service := NewNativeProxyPoolService(nil, proxyRepo, enabledNativeProxyPoolConfig())
	created := false

	err := service.CreateAccount(context.Background(), account, func() error {
		created = true
		return nil
	})

	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, account.ProxyID)
	require.Equal(t, int64(102), *account.ProxyID)
}

func TestNativeProxyPoolServiceSkipsExistingProxyAndNonGPTAccount(t *testing.T) {
	proxyID := int64(77)
	proxyRepo := &nativeProxyPoolProxyRepoStub{proxies: []ProxyWithAccountCount{
		{Proxy: Proxy{ID: 101, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10001"}},
	}}
	service := NewNativeProxyPoolService(nil, proxyRepo, enabledNativeProxyPoolConfig())

	for _, account := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ProxyID: &proxyID},
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: false},
	} {
		called := false
		err := service.CreateAccount(context.Background(), account, func() error {
			called = true
			return nil
		})
		require.NoError(t, err)
		require.True(t, called)
	}
}

func TestNativeProxyPoolServiceReconcilesUnboundEnabledAccounts(t *testing.T) {
	accountRepo := &nativeProxyPoolAccountRepoStub{accounts: []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ProxyID: func() *int64 { v := int64(999); return &v }()},
	}}
	proxyRepo := &nativeProxyPoolProxyRepoStub{proxies: []ProxyWithAccountCount{
		{Proxy: Proxy{ID: 101, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10001"}},
		{Proxy: Proxy{ID: 102, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10002"}},
	}}
	service := NewNativeProxyPoolService(accountRepo, proxyRepo, enabledNativeProxyPoolConfig())

	assigned, err := service.Reconcile(context.Background())

	require.NoError(t, err)
	require.Equal(t, 2, assigned)
	require.Len(t, accountRepo.updated, 2)
	require.Equal(t, int64(102), *accountRepo.updated[0].ProxyID)
	require.Equal(t, int64(101), *accountRepo.updated[1].ProxyID)
}

func TestNativeProxyPoolServiceReportsExhaustionWithoutCreating(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	proxyRepo := &nativeProxyPoolProxyRepoStub{}
	service := NewNativeProxyPoolService(nil, proxyRepo, enabledNativeProxyPoolConfig())
	called := false

	err := service.CreateAccount(context.Background(), account, func() error {
		called = true
		return nil
	})

	require.Error(t, err)
	require.ErrorIs(t, err, errNativeProxyPoolExhausted)
	require.False(t, called)
	require.Nil(t, account.ProxyID)
}

func TestNativeProxyPoolServiceReconcilePropagatesUpdateError(t *testing.T) {
	accountRepo := &nativeProxyPoolAccountRepoStub{
		accounts:  []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}},
		updateErr: errors.New("update failed"),
	}
	proxyRepo := &nativeProxyPoolProxyRepoStub{proxies: []ProxyWithAccountCount{
		{Proxy: Proxy{ID: 101, Protocol: "http", Host: "172.18.0.1", Port: 31289, Username: "native10001"}},
	}}
	service := NewNativeProxyPoolService(accountRepo, proxyRepo, enabledNativeProxyPoolConfig())

	assigned, err := service.Reconcile(context.Background())

	require.Error(t, err)
	require.Zero(t, assigned)
	require.ErrorContains(t, err, "persist native IPv6 proxy")
}
