package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const openAIDownstreamCookieKey = "openai_downstream_cookie"

// Capture before any account-bound headers are applied. Only the response
// writer is adapted; upstream requests and cookie harvesting retain real values.
func beginOpenAIDownstreamCookie(c *gin.Context, account *Account) func(*OpenAIForwardResult) {
	if c == nil || c.Request == nil || c.Writer == nil || account == nil || account.Platform != PlatformOpenAI ||
		GetOpenAIClientTransport(c) == OpenAIClientTransportWS || strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
		return func(*OpenAIForwardResult) {}
	}
	state, _ := c.Get(openAIDownstreamCookieKey)
	w, ok := state.(*openAIDownstreamCookieWriter)
	if !ok {
		w = &openAIDownstreamCookieWriter{
			ResponseWriter: c.Writer,
			requestCookie:  strings.Join(c.Request.Header.Values("Cookie"), "; "),
		}
		for _, cookie := range c.Request.Cookies() {
			if value := cookie.String(); value != "" {
				w.cookies = append(w.cookies, value)
			}
		}
		c.Set(openAIDownstreamCookieKey, w)
		c.Writer = w
	}
	// An uncommitted failover must not reuse the previous attempt's cookies.
	if !w.committed {
		w.upstreamCookies = nil
	}
	return func(result *OpenAIForwardResult) {
		if result != nil {
			result.DownstreamRequestCookie = w.requestCookie
			result.DownstreamResponseCookie = w.responseCookie
		}
	}
}

type openAIDownstreamCookieWriter struct {
	gin.ResponseWriter
	requestCookie   string
	cookies         []string
	upstreamCookies []string
	responseCookie  string
	committed       bool
}

// Capture the actual response before the response-header allowlist removes
// Set-Cookie. Never fall back to the cookie sent in the upstream request.
func captureOpenAIDownstreamResponseCookie(c *gin.Context, resp *http.Response) {
	if c == nil {
		return
	}
	state, _ := c.Get(openAIDownstreamCookieKey)
	w, ok := state.(*openAIDownstreamCookieWriter)
	if !ok || w.committed || w.ResponseWriter.Written() {
		return
	}
	w.upstreamCookies = nil
	if resp == nil {
		return
	}
	for key, values := range resp.Header {
		if strings.EqualFold(key, "Set-Cookie") {
			w.upstreamCookies = append(w.upstreamCookies, values...)
		}
	}
}

func (w *openAIDownstreamCookieWriter) prepare() {
	if w.committed || w.ResponseWriter.Written() {
		return
	}
	// Prefer the client's original cookie. With no inbound cookie, return the
	// actual upstream Set-Cookie values, preserving attributes and duplicates.
	values := w.cookies
	if strings.TrimSpace(w.requestCookie) == "" {
		values = w.upstreamCookies
	}
	for key := range w.Header() {
		if strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "Set-Cookie") {
			delete(w.Header(), key)
		}
	}
	for _, value := range values {
		w.Header().Add("Set-Cookie", value)
	}
	w.responseCookie = strings.Join(w.Header().Values("Set-Cookie"), "\n")
	w.committed = true
}

func (w *openAIDownstreamCookieWriter) WriteHeaderNow() {
	w.prepare()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *openAIDownstreamCookieWriter) Write(p []byte) (int, error) {
	w.prepare()
	return w.ResponseWriter.Write(p)
}

func (w *openAIDownstreamCookieWriter) WriteString(s string) (int, error) {
	w.prepare()
	return w.ResponseWriter.WriteString(s)
}

func (w *openAIDownstreamCookieWriter) Flush() {
	w.prepare()
	w.ResponseWriter.Flush()
}
