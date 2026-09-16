package service

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
)

type mode1RequestSnapshot struct {
	accountID int64
	body      []byte
}

const mode1OriginalRequestKey = "mode1_original_request"

func stageMode1Request(c *gin.Context, a *Account, body []byte) {
	if c == nil {
		return
	}
	c.Set(mode1OriginalRequestKey, (*mode1RequestSnapshot)(nil))
	if a.RequestIntegrityMode() != "off" {
		c.Set(mode1OriginalRequestKey, &mode1RequestSnapshot{a.ID, bytes.Clone(body)})
	}
}

// validateMode1RequestIntegrity rejects lossy compatibility transformations.
// Identity metadata and transport control fields are deliberately outside this
// comparison. A transport cannot pretend that stripped context was preserved.
func validateMode1RequestIntegrity(original, forwarded []byte) error {
	return validateMode1RequestIntegrityForAccount(nil, original, forwarded)
}

// Compare protocol semantics, not JSON shape. Only the documented Codex
// compatibility conversions are allowed. Never run the full (lossy) transform
// on the snapshot: doing so would conceal dropped tools or reasoning items.
func validateMode1RequestIntegrityForAccount(account *Account, original, forwarded []byte) error {
	decode := func(raw []byte) (map[string]any, error) {
		var v map[string]any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		err := d.Decode(&v)
		if err == nil {
			var trailing any
			if v == nil || d.Decode(&trailing) != io.EOF {
				err = io.ErrUnexpectedEOF
			}
		}
		return v, err
	}
	before, err := decode(original)
	if err != nil {
		return infraerrors.BadRequest("MODE1_INVALID_JSON", "模式一请求不是有效 JSON")
	}
	after, err := decode(forwarded)
	if err != nil {
		return infraerrors.BadRequest("MODE1_INVALID_JSON", "模式一出站请求不是有效 JSON")
	}
	codex := account != nil && account.UsesOpenAICodexProtocol()
	if codex {
		hadInstructions := !isInstructionsEmpty(before)
		// The internal subscription endpoint does not accept token budgets.
		// Absence is an explicit protocol limitation; changing a supported
		// budget or losing one on an API-key endpoint is still rejected.
		for _, key := range []string{"max_output_tokens", "max_completion_tokens"} {
			if _, exists := after[key]; !exists {
				delete(before, key)
			}
		}
		if model, ok := before["model"].(string); ok {
			if actual, ok := after["model"].(string); ok && strings.TrimSpace(actual) == normalizeOpenAIModelForUpstream(account, account.GetMappedModel(model)) {
				before["model"] = actual
			}
		}
		mode1CanonicalizeCodexRequest(before)
		mode1CanonicalizeCodexRequest(after)
		if !hadInstructions {
			// HTTP can inject its default before system-message promotion;
			// accept that exact default suffix without relaxing user text.
			if expected, ok := before["instructions"].(string); ok {
				if actual, ok := after["instructions"].(string); ok {
					model, _ := after["model"].(string)
					if actual == expected+"\n\n"+defaultCodexSynthInstructions(model) {
						after["instructions"] = expected
					}
				}
			}
		}
	}
	for _, key := range []string{"model", "input", "instructions", "reasoning", "tools", "tool_choice", "parallel_tool_calls", "text", "previous_response_id", "max_output_tokens", "max_completion_tokens", "session", "functions", "function_call"} {
		if value, exists := before[key]; exists && !reflect.DeepEqual(value, after[key]) {
			return infraerrors.BadRequest("MODE1_LOSSY_TRANSFORM", "模式一阻止了会改变请求语义的转换，字段："+key)
		}
	}
	return nil
}

func validateMode1StagedRequest(c *gin.Context, a *Account, forwarded []byte) error {
	if a.RequestIntegrityMode() == "off" || c == nil {
		return nil
	}
	v, _ := c.Get(mode1OriginalRequestKey)
	s, _ := v.(*mode1RequestSnapshot)
	if s == nil || s.accountID != a.ID {
		return nil
	}
	return checkAccountRequestIntegrity(c, a, s.body, forwarded)
}
