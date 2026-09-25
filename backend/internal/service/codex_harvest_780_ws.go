package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
)

func requestCodex780WS(req *http.Request, proxy, edge string, payload []byte, seed []string, target, model string) (out codexHarvestProbeResult) {
	out = codexHarvestProbeResult{Sent: true, EdgeIP: edge, Transport: "websocket", Gateway: target}
	client, req, err := codexMintHTTPClient(req, proxy, edge)
	if err != nil {
		out.Err = errors.New("mint websocket proxy unavailable")
		return
	}
	defer client.CloseIdleConnections()
	headers := req.Header.Clone()

	headers.Del("Accept")
	headers.Del("Content-Type")
	headers.Set("OpenAI-Beta", openAIWSBetaV2Value)
	endpoint := *req.URL
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	conn, resp, err := coderws.Dial(req.Context(), endpoint.String(), &coderws.DialOptions{HTTPClient: client, Host: req.Host, HTTPHeader: headers, CompressionMode: coderws.CompressionDisabled})
	if resp != nil {
		out.Status = resp.StatusCode
		out.RetryAfter = codexHarvestRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	}
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		out.Err = mintTransportError(err)
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(16 * 1024)
	out.State = extractOpenAICodexTurnState(resp.Header)
	incoming := responseCookiePairs(resp)
	changed := false
	for _, c := range resp.Cookies() {
		if c.Name == "__cflb" || c.Name == "__oailb" {
			changed = true
		}
	}
	if !changed {
		incoming = seed
	}
	out.Cookies, _, err = codex780Route(incoming, target, time.Now())
	if err != nil {
		out.Err = err
		return
	}
	out.Gateway = codex780CookieGateway(out.Cookies)
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		out.Err = errors.New("mint payload invalid")
		return
	}
	delete(body, "stream")
	body["type"] = "response.create"
	raw, _ := json.Marshal(body)
	if err := conn.Write(req.Context(), coderws.MessageText, raw); err != nil {
		out.Err = mintTransportError(err)
		return
	}
	created := false
	for total := 0; total < 64*1024; {
		kind, message, err := conn.Read(req.Context())
		total += len(message)
		if err != nil {
			out.Err = mintTransportError(err)
			return
		}
		if kind != coderws.MessageText || total > 64*1024 {
			out.Err = errors.New("mint websocket response incomplete")
			return
		}
		var event struct {
			Type     string            `json:"type"`
			Headers  map[string]string `json:"headers"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"response"`
		}
		if json.Unmarshal(message, &event) != nil {
			out.Err = errors.New("mint websocket invalid event")
			return
		}
		switch event.Type {
		case "codex.response.metadata":
			if ticket := event.Headers["x-codex-turn-state"]; ticket != "" {
				out.State = ticket
			}
		case "response.created":
			if strings.TrimSpace(event.Response.ID) == "" || event.Response.Model != model {
				out.Err = &codexMintError{kind: "model_mismatch", detail: "mint model declaration mismatch"}
				return
			}
			created = true
		case "error", "response.failed":
			out.Err = errors.New("mint websocket error event")
			return
		}
		if created && out.State != "" {
			return
		}
	}
	out.Err = errors.New("mint websocket scan limit")
	return
}
