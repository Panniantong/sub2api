package streamlatency

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

func TestHTTPTracePreservesConnectionReuseAndExistingHooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
	}))
	defer srv.Close()
	client := srv.Client()
	ctx, trace := NewContext(context.Background())
	for i := 0; i < 2; i++ {
		called := false
		req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { called = true }}), http.MethodPost, srv.URL, nil)
		req, observe := StartUpstream(req)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		observe(resp)
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("existing trace hook lost")
		}
	}
	snapshot := trace.Snapshot()
	if snapshot["connection_reused"] != true || snapshot["upstream_attempts"] != 2 {
		t.Fatalf("reuse/attempts: %v", snapshot)
	}
	if _, ok := snapshot["first_output_ms"]; ok {
		t.Fatal("upstream read counted as downstream output")
	}
	trace.Output("text")
	trace.Flushed()
	snapshot = trace.Snapshot()
	for _, field := range []string{"first_output_ms", "first_text_ms", "upstream_body_first_byte_ms", "relay_first_output_ms"} {
		if _, ok := snapshot[field]; !ok {
			t.Fatalf("missing %s in %v", field, snapshot)
		}
	}
}

func TestTraceIgnoresPreviousAttemptCallbacks(t *testing.T) {
	ctx, trace := NewContext(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", nil)
	oldReq, oldObserve := StartUpstream(req)
	oldResp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"old\"}\n\n"))}
	oldObserve(oldResp)
	defer oldResp.Body.Close()

	currentReq, _ := StartUpstream(req)
	httptrace.ContextClientTrace(currentReq.Context()).GotConn(httptrace.GotConnInfo{Reused: true})
	// A failed attempt's scanner and transport callbacks can finish after retry.
	httptrace.ContextClientTrace(oldReq.Context()).GotConn(httptrace.GotConnInfo{Reused: false})
	httptrace.ContextClientTrace(oldReq.Context()).GotFirstResponseByte()
	if _, err := io.Copy(io.Discard, oldResp.Body); err != nil {
		t.Fatal(err)
	}
	trace.Output("text")
	trace.Flushed()
	got := trace.Snapshot()
	if got["connection_reused"] != true || got["upstream_attempts"] != 2 {
		t.Fatalf("previous attempt overwrote current trace: %v", got)
	}
	for _, key := range []string{"response_header_first_byte_ms", "upstream_body_first_byte_ms", "upstream_first_output_ms", "relay_first_output_ms"} {
		if _, exists := got[key]; exists {
			t.Fatalf("previous attempt contributed %s: %v", key, got)
		}
	}
}

func TestTraceRetryUsesInboundStartAndOmitsUnobservedMetrics(t *testing.T) {
	ctx, trace := NewContext(context.Background())
	trace.started = time.Now().Add(-time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", nil)
	_, _ = StartUpstream(req)
	_, _ = StartUpstream(req)
	trace.Flushed() // heartbeat
	if _, ok := trace.Snapshot()["first_output_ms"]; ok {
		t.Fatal("heartbeat counted")
	}
	trace.Output("reasoning")
	trace.Flushed()
	before := trace.Snapshot()
	if before["first_output_ms"].(float64) < 1000 {
		t.Fatal("request start reset on retry")
	}
	if _, ok := before["first_text_ms"]; ok {
		t.Fatal("reasoning counted as answer text")
	}
	if _, ok := before["relay_first_output_ms"]; ok {
		t.Fatal("missing upstream observation reported as zero")
	}
	trace.Output("text")
	trace.Flushed()
	after := trace.Snapshot()
	if after["first_output_ms"] != before["first_output_ms"] {
		t.Fatal("first output overwritten")
	}
	if _, ok := after["first_text_ms"]; !ok {
		t.Fatal("text not recorded")
	}
}
