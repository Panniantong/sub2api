package service

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Requests are tracked before account selection and released with the user slot.
// This includes account queues/retries, but excludes idle WebSocket connections.
type CookieGroupDemandCache interface {
	TouchCookieGroupDemand(context.Context, int64, string) error
	ReleaseCookieGroupDemand(context.Context, int64, string) error
	ReadCookieGroupDemand(context.Context, int64, int) (CookieGroupDemand, error)
}

type CookieGroupDemand struct {
	Current int
	Peak    int
}

func (s *ConcurrencyService) TrackCookieGroupDemand(ctx context.Context, groupID int64) func() {
	if s == nil || groupID <= 0 {
		return func() {}
	}
	cache, ok := s.cache.(CookieGroupDemandCache)
	if !ok {
		return func() {}
	}
	id := generateRequestID()
	touch := func() {
		callCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = cache.TouchCookieGroupDemand(callCtx, groupID, id)
	}
	touch()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		defer func() {
			callCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = cache.ReleaseCookieGroupDemand(callCtx, groupID, id)
		}()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				touch()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }
}

func (s *ConcurrencyService) cookieGroupDemand(ctx context.Context, groupID int64, seconds int) (CookieGroupDemand, error) {
	if s == nil {
		return CookieGroupDemand{}, fmt.Errorf("concurrency service unavailable")
	}
	cache, ok := s.cache.(CookieGroupDemandCache)
	if !ok {
		return CookieGroupDemand{}, fmt.Errorf("group demand unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return cache.ReadCookieGroupDemand(callCtx, groupID, seconds)
}
