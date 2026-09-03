package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const nativeProxyPoolReconcileInterval = time.Minute
const nativeProxyPoolReconcileTimeout = 15 * time.Second

// NativeProxyPoolService assigns one idle native IPv6 proxy to each newly
// enabled OpenAI OAuth account and repairs existing enabled accounts without a
// proxy. It is intentionally a no-op when the instance-level pool is disabled.
type NativeProxyPoolService struct {
	accountRepo AccountRepository
	proxyRepo   ProxyRepository
	cfg         *config.Config

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func NewNativeProxyPoolService(accountRepo AccountRepository, proxyRepo ProxyRepository, cfg *config.Config) *NativeProxyPoolService {
	return &NativeProxyPoolService{
		accountRepo: accountRepo,
		proxyRepo:   proxyRepo,
		cfg:         cfg,
	}
}

// CreateAccount executes account creation while reserving a unique native
// proxy when this account is eligible. The shared process lock covers both
// proxy selection and the database write, so CRS and admin imports cannot
// reserve the same proxy concurrently in one Sub2API process.
func (s *NativeProxyPoolService) CreateAccount(ctx context.Context, account *Account, create func() error) error {
	if create == nil {
		return errors.New("native proxy pool account create callback is nil")
	}
	if !s.shouldAssign(account) {
		return create()
	}

	nativeProxyPoolProcessMu.Lock()
	defer nativeProxyPoolProcessMu.Unlock()

	allocator, err := s.newAllocator(ctx)
	if err != nil {
		return fmt.Errorf("load native IPv6 proxy pool: %w", err)
	}
	proxyID, err := allocator.acquire()
	if err != nil {
		return fmt.Errorf("allocate native IPv6 proxy: %w", err)
	}
	account.ProxyID = proxyID
	return create()
}

// Reconcile assigns proxies to all currently schedulable OpenAI OAuth
// accounts that still have no proxy. Existing proxy bindings are untouched.
func (s *NativeProxyPoolService) Reconcile(ctx context.Context) (int, error) {
	if !s.enabled() {
		return 0, nil
	}
	if s.accountRepo == nil {
		return 0, errors.New("native IPv6 proxy pool requires account repository")
	}

	nativeProxyPoolProcessMu.Lock()
	defer nativeProxyPoolProcessMu.Unlock()

	accounts, err := s.accountRepo.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return 0, fmt.Errorf("list schedulable OpenAI accounts: %w", err)
	}
	allocator, err := s.newAllocator(ctx)
	if err != nil {
		return 0, fmt.Errorf("load native IPv6 proxy pool: %w", err)
	}

	assigned := 0
	for i := range accounts {
		account := &accounts[i]
		if !s.shouldAssign(account) {
			continue
		}
		proxyID, acquireErr := allocator.acquire()
		if acquireErr != nil {
			return assigned, fmt.Errorf("allocate native IPv6 proxy for account %d: %w", account.ID, acquireErr)
		}
		account.ProxyID = proxyID
		if err := s.accountRepo.Update(ctx, account); err != nil {
			return assigned, fmt.Errorf("persist native IPv6 proxy for account %d: %w", account.ID, err)
		}
		assigned++
	}
	return assigned, nil
}

func (s *NativeProxyPoolService) newAllocator(ctx context.Context) (*nativeProxyPoolAllocator, error) {
	if !s.enabled() {
		return nil, errors.New("native IPv6 proxy pool is disabled")
	}
	if s.proxyRepo == nil {
		return nil, errors.New("native IPv6 proxy pool requires proxy repository")
	}
	proxies, err := s.proxyRepo.ListActiveWithAccountCount(ctx)
	if err != nil {
		return nil, err
	}
	return newNativeProxyPoolAllocator(proxies, s.cfg.Gateway.NativeProxyPool)
}

func (s *NativeProxyPoolService) enabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.NativeProxyPool.Enabled
}

func (s *NativeProxyPoolService) shouldAssign(account *Account) bool {
	return s.enabled() && account != nil && account.ProxyID == nil &&
		account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth &&
		account.Status == StatusActive && account.Schedulable
}

// Start begins the bounded reconciliation loop. Disabled instances do not
// start a goroutine.
func (s *NativeProxyPoolService) Start() {
	if !s.enabled() || s.accountRepo == nil || s.proxyRepo == nil {
		return
	}
	s.startOnce.Do(func() {
		s.stopCh = make(chan struct{})
		s.doneCh = make(chan struct{})
		go s.run()
	})
}

func (s *NativeProxyPoolService) run() {
	defer close(s.doneCh)
	s.reconcileOnce()
	ticker := time.NewTicker(nativeProxyPoolReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.reconcileOnce()
		case <-s.stopCh:
			return
		}
	}
}

func (s *NativeProxyPoolService) reconcileOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), nativeProxyPoolReconcileTimeout)
	defer cancel()
	assigned, err := s.Reconcile(ctx)
	if err != nil {
		slog.Error("native_proxy_pool_reconcile_failed", "assigned", assigned, "error", err)
		return
	}
	if assigned > 0 {
		slog.Info("native_proxy_pool_reconciled", "assigned", assigned)
	}
}

func (s *NativeProxyPoolService) Stop() {
	if s == nil || s.doneCh == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	<-s.doneCh
}
