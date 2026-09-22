package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type turnAdmissionNativeConn struct{ *stagedPassthroughConn }

func (c *turnAdmissionNativeConn) WriteJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, payload)
}

// These regressions are retained in the source tree, independently of the
// immutable diagnostic suites. All upstreams are synthetic/in-memory.
func TestOpenAITurnAdmissionDrainsCurrentRejectsNext(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeDedicated, OpenAIWSIngressModePassthrough} {
		for _, change := range []string{"ticket_expiry", "account_expiry", "ticketless_model"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.OAuthEnabled = true
				cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
				cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{
					Enabled: true, FailClosed: true, Models: []string{"gpt-5.6-sol", "gpt-6-astra"},
				}
				a := ticketTestAccount(731)
				a.Concurrency = 1
				a.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": mode}
				firstModel, nextModel := "gpt-6-astra", "gpt-6-astra"
				expiry := time.Now().Add(650 * time.Millisecond)
				ticketExpiry := expiry
				if change == "account_expiry" {
					a.ExpiresAt, a.AutoPauseOnExpired = &expiry, true
					ticketExpiry = time.Now().Add(time.Hour)
				}
				if change == "ticketless_model" {
					firstModel, ticketExpiry = "gpt-5.6-sol", time.Now().Add(time.Hour)
				}
				a.Extra[openAICodexTicketExtraKey(firstModel)] = &openAICodexTicket{
					AccountID: a.ID, Model: firstModel, State: fakeCodexTicketState(292), Length: 292,
					CapturedAt: time.Now(), IssuedAt: time.Now(), ExpiresAt: ticketExpiry, Identity: ticketIdentity(a),
				}
				upstream := newStagedPassthroughConn()
				svc := newPassthroughLifecycleService(cfg, upstream)
				// Use the same authoritative-reader boundary as the production
				// service.  The prior fixture had no repository, so it could
				// accidentally mask account lifecycle changes by reusing the
				// original in-memory pointer.
				svc.accountRepo = &turnAdmissionRepo{account: a}
				svc.requireLatestTurnAdmission = true
				if mode != OpenAIWSIngressModePassthrough {
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &turnAdmissionNativeConn{upstream}})
					svc.openaiWSPool = pool
					defer pool.Close()
				}
				server, errs := startPassthroughHookRecordingServer(t, ctx, svc, a, nil)
				defer server.Close()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
				require.NoError(t, err)
				defer func() { _ = client.CloseNow() }()
				write := func(model string) {
					require.NoError(t, client.Write(ctx, coderws.MessageText,
						[]byte(fmt.Sprintf(`{"type":"response.create","model":%q,"input":"synthetic"}`, model))))
				}
				readType := func(want string) {
					_, payload, err := client.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, want, gjson.GetBytes(payload, "type").String())
				}
				write(firstModel)
				requirePassthroughUpstreamWrite(t, upstream, time.Second)
				upstream.Send(`{"type":"response.output_text.delta","delta":"started"}`)
				readType("response.output_text.delta")
				if change != "ticketless_model" {
					time.Sleep(time.Until(expiry) + 30*time.Millisecond)
				}
				upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_synthetic","model":%q,"usage":{"input_tokens":1,"output_tokens":1}}}`, firstModel))
				readType("response.completed")
				write(nextModel)
				select {
				case <-upstream.writes:
					t.Fatal("ineligible next turn reached upstream")
				case err := <-errs:
					require.True(t, IsOpenAITurnAdmissionError(err), "%v", err)
				case <-time.After(2 * time.Second):
					t.Fatal("ineligible next turn did not terminate within the test bound")
				}
				require.True(t, a.Schedulable, "admission must not write whole-account pause")
				require.Nil(t, a.TempUnschedulableUntil)
			})
		}
	}
}
