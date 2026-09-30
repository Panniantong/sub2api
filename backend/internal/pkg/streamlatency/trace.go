package streamlatency

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

type contextKey struct{}

// Trace belongs to the inbound HTTP request and survives account failover.
// Durations are diagnostic only; billing and scheduler TTFT keep their contract.
type Trace struct {
	mu            sync.Mutex
	started       time.Time
	values        map[string]any
	attempts      int
	firstOutput   time.Time
	pendingOutput time.Time
	pendingText   time.Time
	flushedOutput bool
	flushedText   bool
}

func NewContext(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{started: time.Now(), values: make(map[string]any)}
	return context.WithValue(ctx, contextKey{}, t), t
}

func FromContext(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(contextKey{}).(*Trace)
	return t
}

func (t *Trace) elapsed(at time.Time) float64 {
	return float64(at.Sub(t.started).Microseconds()) / 1000
}

func (t *Trace) Output(kind string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.pendingOutput.IsZero() && !t.flushedOutput {
		t.pendingOutput = now
		t.values["first_output_kind"] = kind
	}
	if kind == "text" && t.pendingText.IsZero() && !t.flushedText {
		t.pendingText = now
	}
}

// Flushed records complete, usable events only after the underlying writer flushes.
func (t *Trace) Flushed() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if !t.flushedOutput && !t.pendingOutput.IsZero() {
		t.flushedOutput = true
		t.values["first_output_ms"] = t.elapsed(now)
		if !t.firstOutput.IsZero() {
			t.values["relay_first_output_ms"] = float64(now.Sub(t.firstOutput).Microseconds()) / 1000
		}
	}
	if !t.flushedText && !t.pendingText.IsZero() {
		t.flushedText = true
		t.values["first_text_ms"] = t.elapsed(now)
	}
}

func (t *Trace) Snapshot() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	copy := make(map[string]any, len(t.values)+1)
	for k, v := range t.values {
		copy[k] = v
	}
	copy["upstream_attempts"] = t.attempts
	return copy
}

// ObserveWait measures real slot acquisition waits, including failed attempts.
func ObserveWait(ctx context.Context, slot string) func() {
	return Observe(ctx, slot+"_queue_ms")
}

// Observe accumulates a named synchronous phase across attempts.
func Observe(ctx context.Context, key string) func() {
	t := FromContext(ctx)
	if t == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		previous, _ := t.values[key].(float64)
		t.values[key] = previous + float64(time.Since(start).Microseconds())/1000
	}
}

// StartUpstream adds httptrace hooks to the existing transport, preserving reuse,
// TLS fingerprinting, cancellation, and hooks already installed by callers.
func StartUpstream(req *http.Request) (*http.Request, func(*http.Response)) {
	if req == nil {
		return req, func(*http.Response) {}
	}
	t := FromContext(req.Context())
	if t == nil {
		return req, func(*http.Response) {}
	}
	start := time.Now()
	t.mu.Lock()
	t.attempts++
	attempt := t.attempts
	if t.attempts == 1 {
		t.values["pre_upstream_ms"] = t.elapsed(start)
	}
	for _, key := range []string{"connection_acquire_ms", "connection_reused", "dns_ms", "tcp_ms", "tls_ms", "request_written_ms", "response_header_first_byte_ms", "response_ready_ms", "upstream_protocol", "upstream_body_first_byte_ms", "upstream_first_output_ms", "upstream_event_observation_limited"} {
		delete(t.values, key)
	}
	t.firstOutput = time.Time{}
	t.mu.Unlock()
	var mu sync.Mutex
	var connStart, dnsStart, tlsStart time.Time
	connectStarts := make(map[string]time.Time)
	mark := func(key string, value any) {
		t.mu.Lock()
		if t.attempts == attempt {
			t.values[key] = value
		}
		t.mu.Unlock()
	}
	duration := func(key string, from time.Time) {
		if !from.IsZero() {
			mark(key, float64(time.Since(from).Microseconds())/1000)
		}
	}
	hooks := &httptrace.ClientTrace{
		GetConn: func(string) { mu.Lock(); connStart = time.Now(); mu.Unlock() },
		GotConn: func(info httptrace.GotConnInfo) {
			mu.Lock()
			duration("connection_acquire_ms", connStart)
			mu.Unlock()
			mark("connection_reused", info.Reused)
		},
		DNSStart:     func(httptrace.DNSStartInfo) { mu.Lock(); dnsStart = time.Now(); mu.Unlock() },
		DNSDone:      func(httptrace.DNSDoneInfo) { mu.Lock(); duration("dns_ms", dnsStart); mu.Unlock() },
		ConnectStart: func(network, addr string) { mu.Lock(); connectStarts[network+addr] = time.Now(); mu.Unlock() },
		ConnectDone: func(network, addr string, err error) {
			mu.Lock()
			if err == nil {
				duration("tcp_ms", connectStarts[network+addr])
			}
			delete(connectStarts, network+addr)
			mu.Unlock()
		},
		TLSHandshakeStart: func() { mu.Lock(); tlsStart = time.Now(); mu.Unlock() },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { mu.Lock(); duration("tls_ms", tlsStart); mu.Unlock() },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				duration("request_written_ms", start)
			}
		},
		GotFirstResponseByte: func() { duration("response_header_first_byte_ms", start) },
	}
	traced := req.WithContext(httptrace.WithClientTrace(req.Context(), hooks))
	return traced, func(resp *http.Response) {
		if resp == nil || resp.Body == nil {
			return
		}
		duration("response_ready_ms", start)
		mark("upstream_protocol", resp.Proto)
		if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			return
		}
		observer := &SSEObserver{OnOutput: func(string) {
			t.mu.Lock()
			if t.attempts == attempt && t.firstOutput.IsZero() {
				t.firstOutput = time.Now()
				t.values["upstream_first_output_ms"] = float64(t.firstOutput.Sub(start).Microseconds()) / 1000
			}
			t.mu.Unlock()
		}}
		resp.Body = &observedBody{ReadCloser: resp.Body, observer: observer,
			first:   func() { duration("upstream_body_first_byte_ms", start) },
			limited: func() { mark("upstream_event_observation_limited", true) },
		}
	}
}

type observedBody struct {
	io.ReadCloser
	once     sync.Once
	first    func()
	observer *SSEObserver
	limited  func()
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.once.Do(b.first)
		b.observer.Feed(p[:n])
		if b.observer.Limited && b.limited != nil {
			b.limited()
			b.limited = nil
		}
	}
	return n, err
}
