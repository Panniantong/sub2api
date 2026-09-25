package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type streamOnlyProbe struct {
	handled      bool
	body         string
	rejectReason IngressRejectReason
}

// newStreamOnlyRouter 用与网关相同的路由模板注册桩 handler，记录请求是否放行以及 handler 读到的请求体。
func newStreamOnlyRouter(group *service.Group, probe *streamOnlyProbe) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Next()
		probe.rejectReason, _ = GetIngressRejectReason(c)
	})
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: group})
		c.Next()
	})
	r.Use(GroupStreamOnly())
	handle := func(c *gin.Context) {
		probe.handled = true
		if c.Request.Body != nil {
			raw, _ := io.ReadAll(c.Request.Body)
			probe.body = string(raw)
		}
		c.Status(http.StatusOK)
	}
	for _, path := range []string{
		"/v1/messages", "/antigravity/v1/messages", "/v1/chat/completions", "/chat/completions",
		"/v1/responses", "/responses", "/backend-api/codex/responses",
		"/v1/messages/count_tokens", "/v1/responses/*subpath", "/v1/embeddings",
		"/v1beta/models/*modelAction", "/antigravity/v1beta/models/*modelAction",
	} {
		r.POST(path, handle)
	}
	r.GET("/v1/responses", handle)
	return r
}

func serveStreamOnly(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

var streamOnlyGroup = &service.Group{Platform: service.PlatformAnthropic, StreamOnly: true}

func TestGroupStreamOnly_AllowsNonStreamWhenDisabled(t *testing.T) {
	probe := &streamOnlyProbe{}
	r := newStreamOnlyRouter(&service.Group{Platform: service.PlatformAnthropic}, probe)
	w := serveStreamOnly(r, http.MethodPost, "/v1/messages", `{"model":"claude","stream":false}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, probe.handled)
}

func TestGroupStreamOnly_AdmitsOnlyExplicitStreamingOnEveryJSONEntry(t *testing.T) {
	routes := []string{
		"/v1/messages", "/antigravity/v1/messages", "/v1/chat/completions", "/chat/completions",
		"/v1/responses", "/responses", "/backend-api/codex/responses",
	}
	for _, route := range routes {
		probe := &streamOnlyProbe{}
		r := newStreamOnlyRouter(streamOnlyGroup, probe)
		body := `{"model":"m","stream":true,"messages":[]}`
		w := serveStreamOnly(r, http.MethodPost, route, body)
		require.Equal(t, http.StatusOK, w.Code, route)
		require.True(t, probe.handled, route)
		require.Equal(t, body, probe.body, "%s: handler 必须读到完整请求体", route)
	}

	rejected := map[string]string{
		"missing stream":       `{"model":"m","messages":[]}`,
		"stream false":         `{"model":"m","stream":false}`,
		"stream as string":     `{"model":"m","stream":"true"}`,
		"stream as number":     `{"model":"m","stream":1}`,
		"duplicate key":        `{"model":"m","stream":true,"stream":false}`,
		"case variant":         `{"model":"m","stream":true,"Stream":false}`,
		"only case variant":    `{"model":"m","STREAM":true,"stream":null}`,
		"invalid json":         `{"model":"m","stream":true`,
		"not an object":        `[{"stream":true}]`,
		"empty body":           ``,
		"nested stream only":   `{"model":"m","options":{"stream":true}}`,
		"escaped key is false": `{"model":"m","stream":true,"str\u0065am":false}`,
	}
	for _, route := range routes {
		for name, body := range rejected {
			probe := &streamOnlyProbe{}
			r := newStreamOnlyRouter(streamOnlyGroup, probe)
			w := serveStreamOnly(r, http.MethodPost, route, body)
			require.Equal(t, http.StatusBadRequest, w.Code, "%s %s", route, name)
			require.False(t, probe.handled, "%s %s", route, name)
			require.Equal(t, IngressRejectStreamRequired, probe.rejectReason, "%s %s", route, name)
		}
	}
}

func TestGroupStreamOnly_ErrorFollowsEntryProtocol(t *testing.T) {
	r := newStreamOnlyRouter(streamOnlyGroup, &streamOnlyProbe{})

	var anthropic struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(serveStreamOnly(r, http.MethodPost, "/v1/messages", `{"model":"m"}`).Body.Bytes(), &anthropic))
	require.Equal(t, "error", anthropic.Type)
	require.Equal(t, "invalid_request_error", anthropic.Error.Type)
	require.Contains(t, anthropic.Error.Message, `"stream": true`)

	var openai struct {
		Error struct {
			Type  string `json:"type"`
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(serveStreamOnly(r, http.MethodPost, "/v1/chat/completions", `{"model":"m"}`).Body.Bytes(), &openai))
	require.Equal(t, "invalid_request_error", openai.Error.Type)
	require.Equal(t, "stream_required", openai.Error.Code)
	require.Equal(t, "stream", openai.Error.Param)

	var google struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(serveStreamOnly(r, http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent", `{}`).Body.Bytes(), &google))
	require.Equal(t, http.StatusBadRequest, google.Error.Code)
	require.Equal(t, "INVALID_ARGUMENT", google.Error.Status)
	require.Contains(t, google.Error.Message, "streamGenerateContent")
}

func TestGroupStreamOnly_GeminiDecidesByAction(t *testing.T) {
	for path, allowed := range map[string]bool{
		"/v1beta/models/gemini-2.5-pro:generateContent":                   false,
		"/v1beta/models/gemini-2.5-pro/generateContent":                   false,
		"/antigravity/v1beta/models/gemini-2.5-pro:generateContent":       false,
		"/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse":     true,
		"/antigravity/v1beta/models/gemini-2.5-pro:streamGenerateContent": true,
		"/v1beta/models/gemini-2.5-pro:countTokens":                       true,
		"/v1beta/models/gemini-2.5-pro:GenerateContent":                   true, // handler 以不支持的动作拒绝
	} {
		probe := &streamOnlyProbe{}
		r := newStreamOnlyRouter(streamOnlyGroup, probe)
		w := serveStreamOnly(r, http.MethodPost, path, `{"contents":[]}`)
		require.Equal(t, allowed, probe.handled, path)
		if !allowed {
			require.Equal(t, http.StatusBadRequest, w.Code, path)
		}
	}
}

func TestGroupStreamOnly_LeavesEndpointsWithoutStreamingAlone(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/messages/count_tokens"},
		{http.MethodPost, "/v1/responses/compact"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodGet, "/v1/responses"}, // Responses WebSocket 本身就是流式
	} {
		probe := &streamOnlyProbe{}
		r := newStreamOnlyRouter(streamOnlyGroup, probe)
		w := serveStreamOnly(r, tc.method, tc.path, `{"model":"m"}`)
		require.Equal(t, http.StatusOK, w.Code, tc.path)
		require.True(t, probe.handled, tc.path)
	}
}
