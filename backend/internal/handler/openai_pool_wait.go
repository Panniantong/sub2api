package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

const openAIAccountWaitDeadlineKey = "openai_account_wait_deadline"
const openAIAccountWaitPingKey = "openai_account_wait_ping"

// The deadline belongs to the HTTP request, not a selected account or attempt.
// It is not installed on the forwarding context: admission must not cancel an
// already admitted upstream stream when the queue budget expires.
func openAIAccountWaitRemaining(c *gin.Context, timeout time.Duration, start bool) time.Duration {
	now := time.Now()
	if value, ok := c.Get(openAIAccountWaitDeadlineKey); ok {
		deadline := value.(time.Time)
		if start && now.Add(timeout).Before(deadline) {
			deadline = now.Add(timeout)
			c.Set(openAIAccountWaitDeadlineKey, deadline)
		}
		return deadline.Sub(now)
	}
	if start {
		c.Set(openAIAccountWaitDeadlineKey, now.Add(timeout))
	}
	return timeout
}

// Sleep only a short slice, then let the caller run the complete scheduler
// again with the same group, model, exclusions and continuation constraints.
func (h *ConcurrencyHelper) waitOpenAIPoolRetry(c *gin.Context, ctx context.Context, remaining time.Duration, stream bool, started *bool) error {
	if remaining <= 0 {
		return &ConcurrencyError{SlotType: "account", IsTimeout: true}
	}
	if stream && h.pingFormat != "" && h.pingInterval > 0 {
		now := time.Now()
		if value, ok := c.Get(openAIAccountWaitPingKey); !ok {
			c.Set(openAIAccountWaitPingKey, now.Add(h.pingInterval))
		} else if !now.Before(value.(time.Time)) {
			if !*started {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*started = true
			}
			written, err := fmt.Fprint(c.Writer, string(h.pingFormat))
			if err != nil {
				return err
			}
			recordGatewayStreamHeartbeat(c, written)
			c.Writer.Flush()
			c.Set(openAIAccountWaitPingKey, now.Add(h.pingInterval))
		}
	}
	delay := nextBackoff(150 * time.Millisecond)
	if delay > remaining {
		delay = remaining
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return err
		}
		if openAIAccountWaitRemaining(c, remaining, false) <= 0 {
			return &ConcurrencyError{SlotType: "account", IsTimeout: true}
		}
		return nil
	}
}
