package service

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var errNativeProxyPoolExhausted = errors.New("native IPv6 proxy pool exhausted")

type nativeProxyPoolCandidate struct {
	proxy Proxy
	slot  int64
}

type nativeProxyPoolAllocator struct {
	candidates []nativeProxyPoolCandidate
	reserved   map[int64]struct{}
	pool       config.GatewayNativeProxyPoolConfig
}

func newNativeProxyPoolAllocator(proxies []ProxyWithAccountCount, pool config.GatewayNativeProxyPoolConfig) (*nativeProxyPoolAllocator, error) {
	pool.Protocol = strings.ToLower(strings.TrimSpace(pool.Protocol))
	pool.Host = strings.TrimSpace(pool.Host)
	pool.UsernamePrefix = strings.TrimSpace(pool.UsernamePrefix)
	if pool.Protocol == "" || pool.Host == "" || pool.Port <= 0 || pool.UsernamePrefix == "" {
		return nil, errors.New("native IPv6 proxy pool requires protocol, host, port, and username prefix")
	}

	candidates := make([]nativeProxyPoolCandidate, 0, len(proxies))
	for _, item := range proxies {
		if item.AccountCount != 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.Protocol)) != pool.Protocol || item.Host != pool.Host || item.Port != pool.Port {
			continue
		}
		slot, ok := parseNativeProxySlot(item.Username, pool.UsernamePrefix)
		if !ok {
			continue
		}
		candidates = append(candidates, nativeProxyPoolCandidate{proxy: item.Proxy, slot: slot})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].slot != candidates[j].slot {
			return candidates[i].slot > candidates[j].slot
		}
		return candidates[i].proxy.ID > candidates[j].proxy.ID
	})

	return &nativeProxyPoolAllocator{
		candidates: candidates,
		reserved:   make(map[int64]struct{}, len(candidates)),
		pool:       pool,
	}, nil
}

func (a *nativeProxyPoolAllocator) acquire() (*int64, error) {
	for _, candidate := range a.candidates {
		if _, ok := a.reserved[candidate.proxy.ID]; ok {
			continue
		}
		a.reserved[candidate.proxy.ID] = struct{}{}
		proxyID := candidate.proxy.ID
		return &proxyID, nil
	}

	return nil, fmt.Errorf("%w: no unused active proxy matching %s://%s:%d", errNativeProxyPoolExhausted, a.pool.Protocol, a.pool.Host, a.pool.Port)
}

func parseNativeProxySlot(username, prefix string) (int64, bool) {
	suffix, ok := strings.CutPrefix(strings.TrimSpace(username), prefix)
	if !ok || suffix == "" {
		return 0, false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	slot, err := strconv.ParseInt(suffix, 10, 64)
	return slot, err == nil
}

func proxyForCRSCreate(existing *Account, current *int64, allocator *nativeProxyPoolAllocator) (*int64, error) {
	if existing != nil || allocator == nil {
		return current, nil
	}
	return allocator.acquire()
}
