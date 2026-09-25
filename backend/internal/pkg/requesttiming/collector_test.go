package requesttiming

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFinishAndBindingEitherOrder(t *testing.T) {
	for _, finishFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "bind_first", true: "finish_first"}[finishFirst], func(t *testing.T) {
			c := New(time.Now(), 3)
			ctx := With(context.Background(), c)
			body := c.WrapBody(io.NopCloser(strings.NewReader("abc")))
			if _, err := io.ReadAll(body); err != nil {
				t.Fatal(err)
			}
			done := Observe(ctx, "check")
			done()
			done()
			var got Snapshot
			calls := 0
			if finishFirst {
				c.Finish(200, false)
			}
			c.WhenFinished(func(s Snapshot) { got = s; calls++ })
			c.Finish(200, false)
			c.Finish(500, true)
			if calls != 1 || got.Status != 200 || got.BodyBytes != 3 || !got.BodyComplete || len(got.Spans) != 1 {
				t.Fatalf("unexpected snapshot: %+v, calls %d", got, calls)
			}
			Mark(ctx, "too_late")
			if _, ok := got.Events["too_late"]; ok {
				t.Fatal("snapshot mutated")
			}
		})
	}
}
func TestConcurrentCallbacksAndBounds(t *testing.T) {
	c := New(time.Now(), -1)
	ctx := With(context.Background(), c)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				Observe(ctx, "span")()
				Mark(ctx, "one")
				Output(ctx, true, false, "")
			}
		}()
	}
	wg.Wait()
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if len(s.Spans) != 256 || !s.Truncated {
			t.Fatal("missing bounds")
		}
	})
}
func TestHTTPTraceKeepsOuterHooksAndReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	c := New(time.Now(), 0)
	client := server.Client()
	for range 2 {
		req, _ := http.NewRequestWithContext(With(context.Background(), c), "POST", server.URL, strings.NewReader("hello"))
		req, tr := StartAttempt(req, 7, 0)
		resp, err := client.Do(req)
		tr.Response(resp, err)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if len(s.Attempts) != 2 {
			t.Fatal("lost attempts")
		}
		for _, a := range s.Attempts {
			if !a.BodyEOF || a.ResponseBytes != 2 || a.Events["request_written"] == 0 || a.Events["first_byte"] == 0 {
				t.Fatalf("incomplete trace %+v", a)
			}
		}
		if s.Attempts[1].Reused == nil || !*s.Attempts[1].Reused {
			t.Fatal("reuse missing")
		}
	})
}

func TestHeartbeatFlushIsNotOutput(t *testing.T) {
	c := New(time.Now(), 0)
	ctx := With(context.Background(), c)
	c.Written(time.Now(), 4, nil, true)
	OutputFlushed(ctx)
	Output(ctx, true, false, "")
	c.Written(time.Now(), 4, nil, true) // even after parsing, a heartbeat is not output
	c.mu.Lock()
	_, wrong := c.data.Events["first_output_flush"]
	c.mu.Unlock()
	if wrong {
		t.Fatal("heartbeat counted as output")
	}
	OutputFlushed(ctx)
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if _, ok := s.Events["first_output_flush"]; !ok {
			t.Fatal("output flush missing")
		}
	})
}

type cancelOnCloseFixture struct {
	started chan struct{}
	closed  chan struct{}
}

func (b *cancelOnCloseFixture) Read([]byte) (int, error) {
	close(b.started)
	<-b.closed
	return 0, context.Canceled
}
func (b *cancelOnCloseFixture) Close() error { close(b.closed); return nil }
func TestCompletedStreamCleanupDoesNotBecomeCancellation(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", ""} {
		t.Run(terminal, func(t *testing.T) {
			c := New(time.Now(), 0)
			ctx := With(context.Background(), c)
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			req, trace := StartAttempt(req, 1, 0)
			fixture := &cancelOnCloseFixture{started: make(chan struct{}), closed: make(chan struct{})}
			resp := &http.Response{StatusCode: 200, Body: fixture, Request: req}
			trace.Response(resp, nil)
			Output(ResponseContext(ctx, resp), true, true, terminal)
			done := make(chan struct{})
			go func() { _, _ = resp.Body.Read(make([]byte, 1)); close(done) }()
			<-fixture.started
			_ = resp.Body.Close()
			<-done
			c.Finish(200, false)
			c.WhenFinished(func(s Snapshot) {
				a := s.Attempts[0]
				if terminal == "completed" {
					if a.Error != "" || !a.CleanupCanceled {
						t.Fatalf("cleanup misclassified: %+v", a)
					}
				} else if a.Error != "canceled" || a.CleanupCanceled {
					t.Fatalf("real cancellation hidden: %+v", a)
				}
			})
		})
	}
}
