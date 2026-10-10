// Package retrybudget carries a request-local pre-output deadline across
// detached upstream contexts. It never resets when another request succeeds.
package retrybudget

import (
	"context"
	"sync"
	"time"
)

type key struct{}

type Budget struct {
	mu            sync.Mutex
	timer         *time.Timer
	cancel        context.CancelFunc
	requestCancel context.CancelFunc
	stopRequest   func() bool
	ctx           context.Context
	finished      bool
	expired       bool
	output        bool
}

func New(parent context.Context, duration time.Duration) (context.Context, *Budget) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Budget{ctx: ctx, cancel: cancel}
	b.timer = time.AfterFunc(duration, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if !b.finished {
			b.expired = true
			b.finished = true
			b.cancel()
		}
	})
	request, requestCancel := context.WithCancel(parent)
	b.requestCancel = requestCancel
	b.stopRequest = context.AfterFunc(ctx, requestCancel)
	return context.WithValue(request, key{}, b), b
}

// Output ends the pre-output deadline, without canceling normal generation.
func (b *Budget) Output() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.finished {
		b.output = true
		b.finished = true
		b.timer.Stop()
	}
}

func (b *Budget) Expired() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.expired
}

// Detach preserves only budget cancellation. Client disconnect draining keeps
// its existing semantics; the pre-output timer still cancels blocked I/O.
func Detach(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	b, _ := ctx.Value(key{}).(*Budget)
	if b == nil {
		return base, func() {}
	}
	return budgetContext{Context: base, budget: b.ctx}, func() {}
}

type budgetContext struct {
	context.Context
	budget context.Context
}

func (c budgetContext) Done() <-chan struct{} { return c.budget.Done() }
func (c budgetContext) Err() error            { return c.budget.Err() }

// Close releases callbacks once the handler has completed.
func (b *Budget) Close() {
	b.stopRequest()
	b.requestCancel()
	// Once output began, detached upstream work may still be draining usage
	// after a client disconnect. Do not cancel it when the handler returns.
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	b.timer.Stop()
	if !b.output {
		b.cancel()
	}
}
