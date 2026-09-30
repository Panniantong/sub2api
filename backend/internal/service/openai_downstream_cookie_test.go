package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIDownstreamCookieKeepsInternalUpstreamCookie(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("Cookie", "__oailb=client-original; other=one")
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		openAICodexCookieHostExtraKey: "bound.example", openAICodexCookieExtraKey: "__oailb=internal-bound",
	}}
	finish := beginOpenAIDownstreamCookie(c, account)
	upstream := c.Request.Clone(c.Request.Context())
	svc := &OpenAIGatewayService{}
	svc.applyOpenAICodexCookie(account, upstream.Header)
	require.Equal(t, "internal-bound", parseOpenAICodexCookieHeader(upstream.Header.Get("Cookie"))["__oailb"])
	require.Equal(t, "one", parseOpenAICodexCookieHeader(upstream.Header.Get("Cookie"))["other"])
	c.Writer.Header().Set("Set-Cookie", "__oailb=internal-refreshed; Path=/; HttpOnly")
	c.JSON(http.StatusOK, gin.H{"ok": true})
	result := &OpenAIForwardResult{}
	finish(result)
	require.Equal(t, "__oailb=client-original; other=one", result.DownstreamRequestCookie)
	require.Equal(t, "__oailb=client-original\nother=one", result.DownstreamResponseCookie)
	require.NotContains(t, result.DownstreamResponseCookie, "internal")
}

func TestOpenAIDownstreamCookieUsageRetainsBothDirections(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "cookie-directions", Model: "gpt-5.1", Duration: time.Second,
			RequestCookie: "__oailb=internal-bound", UpstreamHeaders: http.Header{"Set-Cookie": {"__oailb=internal-refreshed; Path=/"}},
			DownstreamRequestCookie: "__oailb=client", DownstreamResponseCookie: "__oailb=client"},
		APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}}, User: &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "__oailb=internal-bound", *usageRepo.lastLog.RequestCookie)
	require.Equal(t, "__oailb=internal-refreshed; Path=/", *usageRepo.lastLog.ResponseCookie)
	require.Equal(t, "__oailb=client", *usageRepo.lastLog.DownstreamRequestCookie)
	require.Equal(t, "__oailb=client", *usageRepo.lastLog.DownstreamResponseCookie)
}

func TestOpenAIDownstreamCookieResponseIsolation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, incoming := range []bool{false, true} {
			t.Run(map[bool]string{false: "json", true: "stream"}[stream]+map[bool]string{false: "_absent", true: "_cookie"}[incoming], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				if incoming {
					c.Request.Header.Add("Cookie", "route=client-original; other=one")
					c.Request.Header.Add("Cookie", "third=two")
				}
				finish := beginOpenAIDownstreamCookie(c, &Account{Platform: PlatformOpenAI})
				// Simulate an overridden upstream response, custom header casing,
				// and a retry before anything is committed.
				c.Writer.Header()["set-cookie"] = []string{"route=server-secret; Path=/; HttpOnly"}
				c.Writer.Header().Set("Cookie", "route=server-secret")
				c.Request.Header.Set("Cookie", "route=mutated-later")
				finish = beginOpenAIDownstreamCookie(c, &Account{Platform: PlatformOpenAI})
				c.Writer.Header().Set("Set-Cookie", "route=retry-secret")
				captureOpenAIDownstreamResponseCookie(c, &http.Response{Header: http.Header{"Set-Cookie": {"route=upstream-response; Path=/; HttpOnly", "expired=; Max-Age=0"}}})
				if stream {
					c.Writer.Header().Set("Content-Type", "text/event-stream")
					_, _ = c.Writer.WriteString("data: OK\n\n")
					c.Writer.Flush()
				} else {
					c.JSON(200, gin.H{"result": "OK"})
				}
				result := &OpenAIForwardResult{}
				finish(result)
				response := rec.Result()
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.Contains(t, string(body), "OK")
				require.Empty(t, response.Header.Values("Cookie"))
				if incoming {
					require.Equal(t, []string{"route=client-original", "other=one", "third=two"}, response.Header.Values("Set-Cookie"))
					require.Equal(t, "route=client-original; other=one; third=two", result.DownstreamRequestCookie)
					require.Equal(t, "route=client-original\nother=one\nthird=two", result.DownstreamResponseCookie)
				} else {
					require.Equal(t, []string{"route=upstream-response; Path=/; HttpOnly", "expired=; Max-Age=0"}, response.Header.Values("Set-Cookie"))
					require.Empty(t, result.DownstreamRequestCookie)
					require.Equal(t, "route=upstream-response; Path=/; HttpOnly\nexpired=; Max-Age=0", result.DownstreamResponseCookie)
				}
			})
		}
	}
}

func TestOpenAIDownstreamCookieDoesNotChangeOtherProtocols(t *testing.T) {
	for _, ws := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Request.Header.Set("Cookie", "route=client")
		account := &Account{Platform: PlatformGrok}
		if ws {
			account.Platform = PlatformOpenAI
			c.Request.Header.Set("Upgrade", "websocket")
		}
		writer := c.Writer
		finish := beginOpenAIDownstreamCookie(c, account)
		require.Same(t, writer, c.Writer)
		finish(nil)
	}
}

func TestOpenAIDownstreamCookieRecordsOnlyCommittedHeaders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("Cookie", "route=client")
	finish := beginOpenAIDownstreamCookie(c, &Account{Platform: PlatformOpenAI})
	result := &OpenAIForwardResult{}
	finish(result)
	require.Equal(t, "route=client", result.DownstreamRequestCookie)
	require.Empty(t, result.DownstreamResponseCookie)
	c.Writer.WriteHeaderNow()
	finish(result)
	require.Equal(t, "route=client", result.DownstreamResponseCookie)
}

func TestOpenAIDownstreamCookieFallbackResetsOnRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	account := &Account{Platform: PlatformOpenAI}
	finish := beginOpenAIDownstreamCookie(c, account)
	captureOpenAIDownstreamResponseCookie(c, &http.Response{Header: http.Header{"Set-Cookie": {"failed=attempt"}}})
	finish = beginOpenAIDownstreamCookie(c, account)
	captureOpenAIDownstreamResponseCookie(c, &http.Response{Header: http.Header{}})
	c.Writer.WriteString("OK")
	result := &OpenAIForwardResult{RequestCookie: "bound=internal"}
	finish(result)
	require.Empty(t, result.DownstreamResponseCookie)
	require.Empty(t, c.Writer.Header().Values("Set-Cookie"))
}
