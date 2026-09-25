package requesttiming

import (
	"context"
	"io"
	"time"
)

type inboundBody struct {
	io.ReadCloser
	c *Collector
}

func (c *Collector) WrapBody(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return nil
	}
	return &inboundBody{ReadCloser: body, c: c}
}
func (b *inboundBody) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := b.ReadCloser.Read(p)
	end := time.Now()
	c := b.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finished {
		c.event("body_read_start", start)
		c.data.BodyReadMS += float64(end.Sub(start)) / float64(time.Millisecond)
		c.data.BodyBytes += int64(n)
		if n > 0 {
			c.event("body_first_byte", end)
		}
		if err == io.EOF || (c.data.BodyExpected >= 0 && c.data.BodyBytes >= c.data.BodyExpected) {
			c.data.BodyComplete = true
			c.event("body_received", end)
		}
		if err != nil && err != io.EOF {
			c.event("body_read_error", end)
		}
	}
	return n, err
}

// Written observes application writes, not remote client receipt. Aggregation
// bounds overhead independently of the number of SSE chunks.
func (c *Collector) Written(start time.Time, n int, err error, flush bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	end := time.Now()
	c.data.DownstreamWriteMS += float64(end.Sub(start)) / float64(time.Millisecond)
	c.data.DownstreamBytes += int64(n)
	if n > 0 {
		c.event("first_write", end)
	}
	if err != nil {
		c.data.DownstreamError = true
	}
	if flush {
		c.event("first_flush", end)
	}
}

// OutputFlushed is called by SSE parsers after flushing buffered output, never
// by heartbeat writers. It does not claim the remote client received bytes.
func OutputFlushed(ctx context.Context) {
	c := From(ctx)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || c.data.DownstreamError {
		return
	}
	if tr, ok := ctx.Value(attemptKey{}).(*Trace); ok && tr.c == c {
		if _, seen := c.data.Attempts[tr.index].Events["first_semantic"]; !seen {
			return
		}
	} else if _, seen := c.data.Events["first_semantic"]; !seen {
		return
	}
	c.event("first_output_flush", time.Now())
}
