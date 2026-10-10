package retrybudget

import (
	"context"
	"testing"
	"time"
)

func TestDeadlineCancelsDetachedIO(t *testing.T) {
	ctx, b := New(context.Background(), 20*time.Millisecond)
	defer b.Output()
	upstream, release := Detach(ctx)
	defer release()
	select {
	case <-upstream.Done():
	case <-time.After(time.Second):
		t.Fatal("detached I/O outlived budget")
	}
	if !b.Expired() {
		t.Fatal("deadline not recorded")
	}
}

func TestOutputStopsDeadlineWithoutStoppingStream(t *testing.T) {
	ctx, b := New(context.Background(), 20*time.Millisecond)
	upstream, release := Detach(ctx)
	defer release()
	b.Output()
	select {
	case <-upstream.Done():
		t.Fatal("normal generation canceled")
	case <-time.After(40 * time.Millisecond):
	}
	if b.Expired() {
		t.Fatal("completed pre-output budget expired")
	}
}

func TestRequestBudgetsAreIndependent(t *testing.T) {
	ctx, a := New(context.Background(), 20*time.Millisecond)
	_, b := New(context.Background(), time.Second)
	b.Output()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("another request reset budget")
	}
	if !a.Expired() {
		t.Fatal("request must expire independently")
	}
}

func TestClosePreservesUsageDrainOnlyAfterOutput(t *testing.T) {
	for _, output := range []bool{false, true} {
		ctx, b := New(context.Background(), time.Second)
		upstream, release := Detach(ctx)
		// Legacy callers release immediately after building the request.
		release()
		if upstream.Err() != nil {
			t.Fatal("legacy release canceled request")
		}
		if output {
			b.Output()
		}
		b.Close()
		if (upstream.Err() != nil) == output {
			t.Fatalf("output=%v, upstream err=%v", output, upstream.Err())
		}
	}
}
