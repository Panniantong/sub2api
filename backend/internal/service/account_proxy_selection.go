package service

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRandomProxyUnavailable is returned when an account explicitly requests
// random proxy mode but the active proxy pool has no usable entry.
var ErrRandomProxyUnavailable = errors.New("random proxy pool has no active proxies")

// ResolveRandomProxy assigns a fresh proxy to an account selected for a
// request. The assignment is runtime-only; proxy_mode remains persisted in
// account extra, so the next request can select another proxy.
func ResolveRandomProxy(ctx context.Context, account *Account, selector RandomProxySelector) error {
	if account == nil || !account.IsRandomProxy() {
		return nil
	}
	// Clear any association inherited from a stale snapshot before querying the
	// current pool. If selection fails, callers must not accidentally route
	// through an old fixed proxy.
	account.ProxyID = nil
	account.Proxy = nil
	if selector == nil {
		return ErrRandomProxyUnavailable
	}
	proxy, err := selector.SelectRandomActiveProxy(ctx)
	if err != nil {
		return fmt.Errorf("select random account proxy: %w", err)
	}
	if proxy == nil || !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return ErrRandomProxyUnavailable
	}
	proxyID := proxy.ID
	account.ProxyID = &proxyID
	account.Proxy = proxy
	return nil
}

// ResolveRandomProxyFromSource is a convenience adapter for services whose
// existing dependency is an AccountRepository interface. Implementations that
// support random proxy selection opt in via the optional interface above.
func ResolveRandomProxyFromSource(ctx context.Context, account *Account, source any) error {
	if account == nil || !account.IsRandomProxy() {
		return nil
	}
	selector, _ := source.(RandomProxySelector)
	return ResolveRandomProxy(ctx, account, selector)
}
