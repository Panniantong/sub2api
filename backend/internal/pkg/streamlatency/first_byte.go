package streamlatency

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type firstByteEnabledKey struct{}
type firstByteResultKey struct{}

// EnableFirstByte opts one upstream request into CPA-style body-read timing.
// This metric is independent of debug tracing and semantic output safeguards.
func EnableFirstByte(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), firstByteEnabledKey{}, true))
}

type firstByteResult struct {
	started time.Time
	// Store nanoseconds + 1 so an unobserved body differs from a zero-ms read.
	elapsed atomic.Int64
}

type firstByteBody struct {
	io.ReadCloser
	result *firstByteResult
}

func (b *firstByteBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.result.elapsed.CompareAndSwap(0, time.Since(b.result.started).Nanoseconds()+1)
	}
	return n, err
}

// ObserveFirstByte wraps without reading, buffering, or changing response bytes.
// Callers outside net/http (e.g. plugins) can use it at their request boundary.
func ObserveFirstByte(resp *http.Response, req *http.Request, started time.Time) {
	if resp == nil || resp.Body == nil || req == nil {
		return
	}
	if _, tracked := FirstByteMilliseconds(resp); tracked {
		return
	}
	result := &firstByteResult{started: started}
	resp.Body = &firstByteBody{ReadCloser: resp.Body, result: result}
	if resp.Request != nil {
		req = resp.Request
	}
	resp.Request = req.WithContext(context.WithValue(req.Context(), firstByteResultKey{}, result))
}

// FirstByteMilliseconds returns (nil, true) when tracking is enabled but no
// positive body read occurred. Errors/EOF alone must not invent a measurement.
func FirstByteMilliseconds(resp *http.Response) (*int, bool) {
	if resp == nil || resp.Request == nil {
		return nil, false
	}
	result, ok := resp.Request.Context().Value(firstByteResultKey{}).(*firstByteResult)
	if !ok {
		return nil, false
	}
	ns := result.elapsed.Load()
	if ns == 0 {
		return nil, true
	}
	ms := int((ns - 1) / int64(time.Millisecond))
	return &ms, true
}

type firstByteTransport struct{ base http.RoundTripper }

func (t firstByteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if enabled, _ := req.Context().Value(firstByteEnabledKey{}).(bool); !enabled {
		return t.base.RoundTrip(req)
	}
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		ObserveFirstByte(resp, req, started)
	}
	return resp, err
}

// TrackFirstByteClient preserves the shared transport/pool and every client
// option. Timing begins immediately before RoundTrip, including header waits.
func TrackFirstByteClient(client *http.Client) *http.Client {
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = firstByteTransport{base: base}
	return &clone
}
