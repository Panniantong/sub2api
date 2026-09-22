package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func collectStreamEvents(t *testing.T, chunks []string) []ResponsesStreamEvent {
	t.Helper()
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4-pro")
	var events []ResponsesStreamEvent
	for _, payload := range chunks {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	return events
}

// TestStream_ReasoningOpensItemBeforeDelta guards the bug where a strict client
// (Codex) drops reasoning deltas that reference an item not yet opened.
func TestStream_ReasoningOpensItemBeforeDelta(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	open := map[int]string{} // output_index -> item type
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			require.NotNil(t, e.Item)
			open[e.OutputIndex] = e.Item.Type
		case "response.reasoning_summary_text.delta":
			require.Equalf(t, "reasoning", open[e.OutputIndex], "reasoning delta before its item was opened")
		case "response.output_text.delta":
			require.Equalf(t, "message", open[e.OutputIndex], "text delta before its item was opened")
		}
	}
}

func TestStream_ReasoningOnlyFailsWithoutSynthesizingVisibleText(t *testing.T) {
	for _, finish := range []string{"stop", "length"} {
		t.Run(finish, func(t *testing.T) {
			var chunk ChatCompletionsChunk
			require.NoError(t, json.Unmarshal([]byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"Let me write. Now. OK."}}]}`), &chunk))
			state := NewChatCompletionsToResponsesStreamState("deepseek-v4.1-flash")
			events := ChatCompletionsChunkToResponsesEvents(&chunk, state)
			state.FinishReason = finish
			events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
			require.Empty(t, FinalizeChatCompletionsResponsesStream(state))
			terminalCount := 0
			for _, event := range events {
				require.NotEqual(t, "response.output_text.delta", event.Type)
				require.NotEqual(t, "response.completed", event.Type)
				if event.Type == "response.failed" {
					terminalCount++
					require.Equal(t, "failed", event.Response.Status)
					require.Equal(t, "upstream_reasoning_only", event.Response.Error.Code)
					require.Equal(t, state.ReasoningItemID, event.Response.Output[0].ID)
					require.Empty(t, event.Response.Output[1].Content[0].Text)
				}
			}
			require.Equal(t, 1, terminalCount)
		})
	}
}

func TestStream_ReasoningOnlyBlankDoesNotSynthesizeVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"   "}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})

	for _, e := range events {
		require.NotEqual(t, "response.output_text.delta", e.Type)
		if e.Type == "response.completed" {
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "message", e.Response.Output[1].Type)
			require.Equal(t, "", e.Response.Output[1].Content[0].Text)
		}
	}
}

func TestStream_ReasoningThenContentDoesNotDuplicateFallbackText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"private plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"final answer"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})

	var textDeltas []string
	for _, e := range events {
		switch e.Type {
		case "response.output_text.delta":
			textDeltas = append(textDeltas, e.Delta)
		case "response.completed":
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "private plan", e.Response.Output[0].Summary[0].Text)
			require.Equal(t, "final answer", e.Response.Output[1].Content[0].Text)
		}
	}
	require.Equal(t, []string{"final answer"}, textDeltas)
}

func TestStream_ReasoningThenToolCallDoesNotSynthesizeVisibleText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"call a tool"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	for _, e := range events {
		require.NotEqual(t, "response.output_text.delta", e.Type)
		if e.Type == "response.completed" {
			require.NotNil(t, e.Response)
			require.Len(t, e.Response.Output, 2)
			require.Equal(t, "reasoning", e.Response.Output[0].Type)
			require.Equal(t, "function_call", e.Response.Output[1].Type)
		}
	}
}

// TestStream_ToolCallLifecycleComplete guards that a tool call is fully closed
// (function_call_arguments.done + output_item.done with full arguments), which
// codex needs to execute the call.
func TestStream_ToolCallLifecycleComplete(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	})

	var sawAdded, sawArgsDone, sawItemDone bool
	for _, e := range events {
		switch e.Type {
		case "response.output_item.added":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawAdded = true
			}
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{"cmd":"ls"}`, e.Arguments)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawItemDone = true
				require.Equal(t, `{"cmd":"ls"}`, e.Item.Arguments)
				require.Equal(t, "call_a", e.Item.CallID)
			}
		}
	}
	require.True(t, sawAdded, "function_call output_item.added missing")
	require.True(t, sawArgsDone, "function_call_arguments.done missing")
	require.True(t, sawItemDone, "function_call output_item.done missing")
}

// TestStream_ToolCallArgumentsInFirstChunkNotDoubled guards the GLM/Zhipu shape
// where a single tool_call delta chunk carries id+name+arguments together.
// Earlier code copied the whole tool_call (including arguments) into state and
// then accumulated the same chunk's arguments again, producing a doubled,
// invalid JSON like {"cmd":"ls"}{"cmd":"ls"} that breaks Codex tool parsing
// ("trailing characters").
func TestStream_ToolCallArgumentsInFirstChunkNotDoubled(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	var argsDelta strings.Builder
	var sawArgsDone, sawItemDone bool
	for _, e := range events {
		switch e.Type {
		case "response.function_call_arguments.delta":
			_, _ = argsDelta.WriteString(e.Delta)
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{"cmd":"ls"}`, e.Arguments)
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawItemDone = true
				require.Equal(t, `{"cmd":"ls"}`, e.Item.Arguments)
			}
		}
	}
	require.True(t, sawArgsDone, "function_call_arguments.done missing")
	require.True(t, sawItemDone, "function_call output_item.done missing")
	// Accumulated deltas must equal the final arguments exactly (no duplication).
	require.Equal(t, `{"cmd":"ls"}`, argsDelta.String())
}

func TestStream_InvalidToolArgumentsAreRejectedBeforeFinalize(t *testing.T) {
	idx := 0
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4-flash")
	chunk := &ChatCompletionsChunk{
		Choices: []ChatChunkChoice{
			{
				Index: 0,
				Delta: ChatDelta{
					ToolCalls: []ChatToolCall{
						{
							Index: &idx,
							ID:    "call_bad",
							Type:  "function",
							Function: ChatFunctionCall{
								Name:      "exec_command",
								Arguments: `{"cmd": "ssh root@HOST`,
							},
						},
					},
				},
			},
		},
	}
	ChatCompletionsChunkToResponsesEvents(chunk, state)

	err := state.ValidateToolCallArguments()
	require.ErrorContains(t, err, "invalid JSON")
}

func TestStream_ValidToolCallAtOutputLimitKeepsIncompleteResponse(t *testing.T) {
	idx := 0
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4-flash")
	chunk := &ChatCompletionsChunk{
		Choices: []ChatChunkChoice{
			{
				Index: 0,
				Delta: ChatDelta{
					ToolCalls: []ChatToolCall{
						{
							Index: &idx,
							ID:    "call_at_limit",
							Type:  "function",
							Function: ChatFunctionCall{
								Name:      "exec_command",
								Arguments: `{}`,
							},
						},
					},
				},
			},
		},
	}
	ChatCompletionsChunkToResponsesEvents(chunk, state)
	state.FinishReason = "length"

	require.NoError(t, state.ValidateToolCallArguments())
	events := FinalizeChatCompletionsResponsesStream(state)
	var sawArgsDone, sawIncomplete bool
	for _, event := range events {
		switch event.Type {
		case "response.function_call_arguments.done":
			sawArgsDone = true
			require.Equal(t, `{}`, event.Arguments)
		case "response.completed":
			require.NotNil(t, event.Response)
			sawIncomplete = event.Response.Status == "incomplete"
		}
	}
	require.True(t, sawArgsDone)
	require.True(t, sawIncomplete)
}

// TestStream_SSEWireComplete drives the full stream through SSE encoding and
// asserts the function_call events carry complete fields on the wire.
func TestStream_SSEWireComplete(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	var addedLine string
	for _, e := range events {
		sse, err := ResponsesEventToSSE(e)
		require.NoError(t, err)
		if e.Type == "response.output_item.added" && e.Item != nil && e.Item.Type == "function_call" {
			addedLine = sse
		}
	}
	require.NotEmpty(t, addedLine)
	// The function_call added event must carry arguments:"" on the wire.
	require.True(t, strings.Contains(addedLine, `"arguments":""`), "added line missing arguments: %s", addedLine)
	require.Contains(t, addedLine, `"call_id":"call_a"`)
}

// completedUsageFromSSE 取出 response.completed 事件在 wire 上的 usage 原始 JSON，
// 返回空串表示没有该事件或没有 usage。
func completedUsageFromSSE(t *testing.T, events []ResponsesStreamEvent) string {
	t.Helper()
	for _, e := range events {
		if e.Type != "response.completed" {
			continue
		}
		sse, err := ResponsesEventToSSE(e)
		require.NoError(t, err)
		payload := strings.TrimSpace(strings.SplitN(sse, "data: ", 2)[1])
		var frame struct {
			Response struct {
				Usage json.RawMessage `json:"usage"`
			} `json:"response"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &frame))
		return string(frame.Response.Usage)
	}
	return ""
}

// TestStream_CompletedUsageKeepsStrictDetailFields 覆盖 Codex 对用量明细的严格解析：
// ResponseCompletedOutputTokensDetails.reasoning_tokens 与
// ResponseCompletedInputTokensDetails.cached_tokens 在客户端都是必填字段。
// 由于 ResponsesOutputTokensDetails 的 omitempty 不作用于内层字段，零值会被省略，
// wire 上出现 `output_tokens_details:{}` 就让 Codex 报
// `missing field `reasoning_tokens“，整条 response.completed 解析失败、流被断开重连。
func TestStream_CompletedUsageKeepsStrictDetailFields(t *testing.T) {
	tests := []struct {
		name         string
		usagePayload string
		wantUsage    string
	}{
		{
			// 上游给了明细但推理计数为 0：必须显式输出 reasoning_tokens:0，
			// 不能退化成空明细对象。
			name:         "zero reasoning tokens stays explicit",
			usagePayload: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":0}}`,
			wantUsage:    `{"input_tokens":10,"output_tokens":5,"total_tokens":15,"output_tokens_details":{"reasoning_tokens":0}}`,
		},
		{
			// 只有 cache_write 明细时 cached_tokens 为 0，也必须显式输出，
			// 否则 Codex 报 missing field `cached_tokens`。
			name:         "zero cached tokens stays explicit alongside cache write",
			usagePayload: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":200}}`,
			wantUsage:    `{"input_tokens":10,"output_tokens":5,"total_tokens":15,"cache_creation_input_tokens":200,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":200}}`,
		},
		{
			name:         "non-zero reasoning tokens keep their value",
			usagePayload: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":7}}`,
			wantUsage:    `{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":7}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := collectStreamEvents(t, []string{
				`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":` + tt.usagePayload + `}`,
			})

			require.Equal(t, tt.wantUsage, completedUsageFromSSE(t, events))
		})
	}
}

// TestStream_CompletedUsageOmitsAbsentDetails 确认上游没有明细时不会被补出误导性的
// 空明细对象：字段整体缺席是安全的，出现空对象才致命。
func TestStream_CompletedUsageOmitsAbsentDetails(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	})

	usage := completedUsageFromSSE(t, events)
	require.Equal(t, `{"input_tokens":10,"output_tokens":5,"total_tokens":15}`, usage)
	require.NotContains(t, usage, `"output_tokens_details":{}`)
	require.NotContains(t, usage, `"input_tokens_details":{}`)
}
