package service

import (
	"context"
	"sync"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// Passthrough may carry many independent response.create turns. Count each
// turn, release at its terminal event, and do not charge session.update/pings.
type accountTrafficFrameConn struct {
	inner   openaiwsv2.FrameConn
	control *AccountTrafficService
	plan    AccountTrafficPlan
	ctx     context.Context
	mu      sync.Mutex
	permit  *AccountTrafficPermit
}

func (c *accountTrafficFrameConn) finish(status int) {
	c.mu.Lock()
	p := c.permit
	c.permit = nil
	c.mu.Unlock()
	p.Finish(status)
}
func (c *accountTrafficFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	kind, payload, err := c.inner.ReadFrame(ctx)
	if err != nil {
		c.finish(0)
		return kind, payload, err
	}
	if status, terminal := accountTrafficEventStatus(payload); terminal {
		c.finish(status)
	}
	return kind, payload, nil
}
func (c *accountTrafficFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	if gjson.GetBytes(payload, "type").String() != "response.create" {
		return c.inner.WriteFrame(ctx, kind, payload)
	}
	c.mu.Lock()
	if c.permit != nil {
		c.mu.Unlock()
		return (&AccountTrafficLimitError{Reason: "上一轮请求尚未结束，请等待后重试", RetryAfter: time.Second}).FailoverError()
	}
	_, p, err := c.control.Begin(c.ctx, c.plan, func() { _ = c.inner.Close() })
	if err != nil {
		c.mu.Unlock()
		if failure := AccountTrafficFailover(err); failure != nil {
			return failure
		}
		return err
	}
	c.permit = p
	c.mu.Unlock()
	err = c.inner.WriteFrame(ctx, kind, payload)
	if err != nil {
		c.finish(0)
	}
	return err
}
func (c *accountTrafficFrameConn) Close() error { c.finish(0); return c.inner.Close() }
func wrapAccountTrafficFrameConn(ctx context.Context, upstream HTTPUpstream, a *Account, inner openaiwsv2.FrameConn) (openaiwsv2.FrameConn, error) {
	plan, err := AccountTrafficPlanFor(a)
	if err != nil {
		return nil, err
	}
	if !plan.Policy.Enabled() {
		return inner, nil
	}
	return &accountTrafficFrameConn{inner: inner, control: accountTrafficController(upstream), plan: plan, ctx: ctx}, nil
}
