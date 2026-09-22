//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatFallbackReasoningVisibilityAndUsage(t *testing.T) {
	const reasoning = "private upstream planning"
	for _, stream := range []bool{false, true} {
		for _, summary := range []string{"none", "auto", "omitted", "null", "absent-reasoning"} {
			for _, outcome := range []string{"answer", "tool", "reasoning-only"} {
				t.Run(fmt.Sprintf("stream=%t/summary=%s/%s", stream, summary, outcome), func(t *testing.T) {
					gin.SetMode(gin.TestMode)
					body := []byte(fmt.Sprintf(`{"model":"deepseek-reasoner","input":"hello","stream":%t,"reasoning":{"effort":"high","summary":%q},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`, stream, summary))
					switch summary {
					case "omitted":
						body = bytes.ReplaceAll(body, []byte(`,"summary":"omitted"`), nil)
					case "null":
						body = bytes.ReplaceAll(body, []byte(`"summary":"null"`), []byte(`"summary":null`))
					case "absent-reasoning":
						body = bytes.ReplaceAll(body, []byte(`,"reasoning":{"effort":"high","summary":"absent-reasoning"}`), nil)
					}
					message := map[string]any{"role": "assistant", "reasoning_content": reasoning}
					if outcome == "answer" {
						message["content"] = "final answer"
					}
					if outcome == "tool" {
						message["tool_calls"] = []any{map[string]any{"index": 0, "id": "call_lookup", "type": "function", "function": map[string]string{"name": "lookup", "arguments": "{}"}}}
					}
					usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 7, "total_tokens": 17, "completion_tokens_details": map[string]int{"reasoning_tokens": 5}}
					finish := "stop"
					if outcome == "tool" {
						finish = "tool_calls"
					}
					var payload string
					if stream {
						chunk, err := json.Marshal(map[string]any{"id": "chat_test", "choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}, "usage": usage})
						require.NoError(t, err)
						payload = "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
					} else {
						data, err := json.Marshal(map[string]any{"id": "chat_test", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage})
						require.NoError(t, err)
						payload = string(data)
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}}
					cache := &reasoningRecordingCache{}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, cache: cache}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
					result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
					require.NoError(t, err, "do not trigger account failover or replay after output")
					require.NotNil(t, result)
					require.Len(t, upstream.requests, 1)
					wire := recorder.Body.String()
					if summary != "auto" {
						require.NotContains(t, wire, reasoning)
						require.NotContains(t, wire, "response.reasoning_summary_")
					} else {
						require.Contains(t, wire, reasoning)
					}
					var response apicompat.ResponsesResponse
					if stream {
						terminals := 0
						for _, line := range strings.Split(wire, "\n") {
							if !strings.HasPrefix(line, "data: ") {
								continue
							}
							frame := strings.TrimPrefix(line, "data: ")
							kind := gjson.Get(frame, "type").String()
							if kind == "response.failed" || kind == "response.completed" {
								terminals++
								require.NoError(t, json.Unmarshal([]byte(gjson.Get(frame, "response").Raw), &response))
							}
							if outcome == "reasoning-only" {
								require.NotEqual(t, "response.output_text.delta", kind)
							}
						}
						require.Equal(t, 1, terminals)
					} else {
						require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
					}
					require.NotNil(t, response.Usage)
					require.NotNil(t, response.Usage.OutputTokensDetails)
					require.Equal(t, 5, response.Usage.OutputTokensDetails.ReasoningTokens)
					require.Equal(t, 7, response.Usage.OutputTokens)
					require.Equal(t, 17, response.Usage.TotalTokens)
					if outcome == "reasoning-only" {
						require.Equal(t, "failed", response.Status)
						require.Equal(t, "upstream_reasoning_only", response.Error.Code)
					} else {
						require.Equal(t, "completed", response.Status)
					}
					sets := cache.snapshotSets()
					require.Len(t, sets, 1)
					var history []json.RawMessage
					for _, item := range response.Output {
						if item.Type == "reasoning" {
							require.Equal(t, reasoning, sets[item.ID], "final item ID must match cached and streamed ID")
							if summary != "auto" {
								require.Empty(t, item.Summary)
							}
						}
						if item.Type == "message" {
							for _, part := range item.Content {
								require.NotContains(t, part.Text, reasoning)
							}
						}
						data, err := json.Marshal(item)
						require.NoError(t, err)
						if item.Type == "reasoning" && summary != "auto" {
							require.Equal(t, "[]", gjson.GetBytes(data, "summary").Raw)
						}
						history = append(history, data)
					}
					if outcome == "tool" {
						history = append(history, json.RawMessage(`{"type":"function_call_output","call_id":"call_lookup","output":"done"}`))
						input, err := json.Marshal(history)
						require.NoError(t, err)
						next, err := apicompat.ResponsesToChatCompletionsRequestWithOptions(&apicompat.ResponsesRequest{Input: input}, &apicompat.ResponsesToChatOptions{ReasoningContentByID: func(id string) string { return sets[id] }})
						require.NoError(t, err)
						var restored bool
						for _, msg := range next.Messages {
							if len(msg.ToolCalls) > 0 {
								require.Equal(t, reasoning, msg.ReasoningContent)
								restored = true
							}
						}
						require.True(t, restored)
					}
				})
			}
		}
	}
}
