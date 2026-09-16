package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type randomProxySelectorStub struct {
	proxy *Proxy
	err   error
	calls int
}

// randomHydrationRepo only supplies the optional selector capability. The
// embedded interface keeps this test independent of the many account-repository
// methods unrelated to request-time proxy resolution.
type randomHydrationRepo struct {
	AccountRepository
	selector *randomProxySelectorStub
}

func (r *randomHydrationRepo) SelectRandomActiveProxy(ctx context.Context) (*Proxy, error) {
	return r.selector.SelectRandomActiveProxy(ctx)
}

func (s *randomProxySelectorStub) SelectRandomActiveProxy(context.Context) (*Proxy, error) {
	s.calls++
	return s.proxy, s.err
}

// rotatingProxySelectorStub cycles through a fixed pool so tests can assert
// that repeated request-time resolution actually rotates proxies instead of
// sticking to the first one sampled.
type rotatingProxySelectorStub struct {
	proxies []*Proxy
	calls   int
}

func (s *rotatingProxySelectorStub) SelectRandomActiveProxy(context.Context) (*Proxy, error) {
	if len(s.proxies) == 0 {
		return nil, nil
	}
	proxy := s.proxies[s.calls%len(s.proxies)]
	s.calls++
	return proxy, nil
}

func randomProxyAccount() *Account {
	return &Account{
		ID:    42,
		Extra: map[string]any{ProxyModeExtraKey: ProxyModeRandom},
	}
}

func TestResolveRandomProxyAssignsEphemeralProxy(t *testing.T) {
	proxy := &Proxy{ID: 7, Status: StatusActive}
	selector := &randomProxySelectorStub{proxy: proxy}
	account := randomProxyAccount()

	if err := ResolveRandomProxy(context.Background(), account, selector); err != nil {
		t.Fatalf("ResolveRandomProxy() error = %v", err)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls = %d, want 1", selector.calls)
	}
	if account.Proxy != proxy || account.ProxyID == nil || *account.ProxyID != proxy.ID {
		t.Fatalf("account proxy was not assigned: proxy=%#v proxy_id=%v", account.Proxy, account.ProxyID)
	}
}

func TestResolveRandomProxyFailsClosedWhenPoolUnavailable(t *testing.T) {
	cases := []struct {
		name string
		stub *randomProxySelectorStub
	}{
		{name: "nil selector", stub: nil},
		{name: "empty pool", stub: &randomProxySelectorStub{}},
		{name: "inactive proxy", stub: &randomProxySelectorStub{proxy: &Proxy{ID: 1, Status: StatusDisabled}}},
		{name: "expired proxy", stub: &randomProxySelectorStub{proxy: &Proxy{ID: 1, Status: StatusActive, ExpiresAt: accountProxyTimePtr(time.Now().Add(-time.Minute))}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := randomProxyAccount()
			account.ProxyID = accountProxyInt64Ptr(99)
			account.Proxy = &Proxy{ID: 99, Status: StatusActive}
			var err error
			if tc.stub == nil {
				err = ResolveRandomProxy(context.Background(), account, nil)
			} else {
				err = ResolveRandomProxy(context.Background(), account, tc.stub)
			}
			if !errors.Is(err, ErrRandomProxyUnavailable) {
				t.Fatalf("error = %v, want ErrRandomProxyUnavailable", err)
			}
			// A failed refresh must not leave a stale fixed proxy usable.
			if account.ProxyID != nil || account.Proxy != nil {
				t.Fatalf("failed selection left a stale proxy association")
			}
		})
	}
}

func TestResolveRandomProxyRotatesAcrossCalls(t *testing.T) {
	proxies := []*Proxy{
		{ID: 21, Status: StatusActive},
		{ID: 22, Status: StatusActive},
		{ID: 23, Status: StatusActive},
	}
	selector := &rotatingProxySelectorStub{proxies: proxies}

	seen := map[int64]int{}
	const resolves = 30
	for i := 0; i < resolves; i++ {
		account := randomProxyAccount()
		if err := ResolveRandomProxy(context.Background(), account, selector); err != nil {
			t.Fatalf("ResolveRandomProxy() error = %v", err)
		}
		if account.Proxy == nil || account.ProxyID == nil {
			t.Fatal("rotating selection left the account without a proxy")
		}
		seen[*account.ProxyID]++
	}
	for _, proxy := range proxies {
		if seen[proxy.ID] == 0 {
			t.Fatalf("proxy %d was never selected in %d resolves (seen=%v)", proxy.ID, resolves, seen)
		}
	}
}

func TestNormalizeProxyModeExtra(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		want  map[string]any
	}{
		{name: "nil map stays nil", input: nil, want: nil},
		{name: "missing key untouched", input: map[string]any{"other": 1}, want: map[string]any{"other": 1}},
		{name: "random kept", input: map[string]any{ProxyModeExtraKey: "random"}, want: map[string]any{ProxyModeExtraKey: "random"}},
		{name: "case and whitespace canonicalized", input: map[string]any{ProxyModeExtraKey: " Random "}, want: map[string]any{ProxyModeExtraKey: "random"}},
		{name: "other string dropped", input: map[string]any{ProxyModeExtraKey: "fixed", "other": 1}, want: map[string]any{"other": 1}},
		{name: "empty string dropped", input: map[string]any{ProxyModeExtraKey: ""}, want: map[string]any{}},
		{name: "non-string dropped", input: map[string]any{ProxyModeExtraKey: 7}, want: map[string]any{}},
		{name: "nil value dropped", input: map[string]any{ProxyModeExtraKey: nil}, want: map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeProxyModeExtra(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("NormalizeProxyModeExtra() = %v, want %v", got, tc.want)
			}
			for key, wantValue := range tc.want {
				if got[key] != wantValue {
					t.Fatalf("NormalizeProxyModeExtra()[%q] = %v, want %v", key, got[key], wantValue)
				}
			}
		})
	}
}

func TestResolveRandomProxyIgnoresNonRandomAccount(t *testing.T) {
	selector := &randomProxySelectorStub{proxy: &Proxy{ID: 1, Status: StatusActive}}
	account := &Account{ID: 1, Extra: map[string]any{ProxyModeExtraKey: "fixed"}}

	if err := ResolveRandomProxy(context.Background(), account, selector); err != nil {
		t.Fatalf("ResolveRandomProxy() error = %v", err)
	}
	if selector.calls != 0 {
		t.Fatalf("selector calls = %d, want 0", selector.calls)
	}
}

func TestGatewayHydrationResolvesRandomProxyWithoutSnapshot(t *testing.T) {
	selector := &randomProxySelectorStub{proxy: &Proxy{ID: 11, Status: StatusActive}}
	repo := &randomHydrationRepo{selector: selector}
	service := &GatewayService{accountRepo: repo}
	account := randomProxyAccount()
	account.ProxyID = accountProxyInt64Ptr(99)
	account.Proxy = &Proxy{ID: 99, Status: StatusActive}

	hydrated, err := service.hydrateSelectedAccount(context.Background(), account)
	if err != nil {
		t.Fatalf("hydrateSelectedAccount() error = %v", err)
	}
	if hydrated != account || hydrated.Proxy == nil || hydrated.Proxy.ID != 11 {
		t.Fatalf("hydrated proxy = %#v, want proxy 11", hydrated.Proxy)
	}
}

func TestOpenAIHydrationResolvesRandomProxyWithoutSnapshot(t *testing.T) {
	selector := &randomProxySelectorStub{proxy: &Proxy{ID: 12, Status: StatusActive}}
	repo := &randomHydrationRepo{selector: selector}
	service := &OpenAIGatewayService{accountRepo: repo}

	hydrated, err := service.hydrateSelectedAccount(context.Background(), randomProxyAccount())
	if err != nil {
		t.Fatalf("hydrateSelectedAccount() error = %v", err)
	}
	if hydrated == nil || hydrated.Proxy == nil || hydrated.Proxy.ID != 12 {
		t.Fatalf("hydrated proxy = %#v, want proxy 12", hydrated.Proxy)
	}
}

func TestGeminiHydrationResolvesRandomProxyWithoutSnapshot(t *testing.T) {
	selector := &randomProxySelectorStub{proxy: &Proxy{ID: 13, Status: StatusActive}}
	repo := &randomHydrationRepo{selector: selector}
	service := &GeminiMessagesCompatService{accountRepo: repo}

	hydrated, err := service.hydrateSelectedAccount(context.Background(), randomProxyAccount())
	if err != nil {
		t.Fatalf("hydrateSelectedAccount() error = %v", err)
	}
	if hydrated == nil || hydrated.Proxy == nil || hydrated.Proxy.ID != 13 {
		t.Fatalf("hydrated proxy = %#v, want proxy 13", hydrated.Proxy)
	}
}

func accountProxyTimePtr(value time.Time) *time.Time { return &value }

func accountProxyInt64Ptr(value int64) *int64 { return &value }
