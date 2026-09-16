package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMode1CodexSemanticEquivalence(t *testing.T) {
	account := mode1TestAccount(1)
	cases := []struct{ name, before, after string }{
		{"string input", `{"input":"hello"}`, `{"input":[{"type":"message","role":"user","content":"hello"}]}`},
		{"text parts", `{"input":[{"role":"user","content":"hello"}]}`, `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`},
		{"reasoning replay metadata", `{"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"}]}`, `{"input":[{"type":"reasoning","summary":[],"encrypted_content":"opaque"}]}`},
		{"system promotion", `{"instructions":"original","input":[{"role":"system","content":"policy"},{"role":"user","content":"hello"}]}`, `{"instructions":"policy\n\noriginal","input":[{"role":"user","content":"hello"}]}`},
		{"tool schema", `{"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`, `{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`},
		{"tool role result", `{"input":[{"role":"tool","tool_call_id":"call_123","content":"answer"}]}`, `{"input":[{"type":"function_call_output","call_id":"fc_123","output":"answer"}]}`},
		{"call pair", `{"input":[{"type":"function_call","call_id":"call_123","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_123","output":"answer"}]}`, `{"input":[{"type":"function_call","call_id":"fc_123","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"fc_123","output":"answer"}]}`},
		{"previous turn reference", `{"input":[{"type":"item_reference","id":"call_123"}]}`, `{"input":[{"type":"item_reference","id":"fc_123"}]}`},
		{"model alias", `{"model":"gpt-5.4-high"}`, `{"model":"gpt-5.4"}`},
		{"minimal compatibility", `{"reasoning":{"effort":"minimal","summary":"auto"}}`, `{"reasoning":{"effort":"none","summary":"auto"}}`},
		{"legacy pro mode", `{"model":"gpt-5.4","reasoning":{"mode":"pro"}}`, `{"model":"gpt-5.4","reasoning":{"effort":"max"}}`},
		{"unsupported OAuth budget", `{"max_output_tokens":1234}`, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, validateMode1RequestIntegrityForAccount(account, []byte(tc.before), []byte(tc.after)))
		})
	}
}

func TestMode1SemanticGuardStillRejectsActualLoss(t *testing.T) {
	cases := []struct{ name, before, after, field string }{
		{"changed text", `{"input":"hello"}`, `{"input":[{"role":"user","content":"goodbye"}]}`, "input"},
		{"dropped turn", `{"input":[{"role":"user","content":"one"},{"role":"assistant","content":"two"}]}`, `{"input":[{"role":"user","content":"one"}]}`, "input"},
		{"lost system", `{"input":[{"role":"system","content":"policy"},{"role":"user","content":"hello"}]}`, `{"input":[{"role":"user","content":"hello"}]}`, "instructions"},
		{"lost encrypted reasoning", `{"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"}]}`, `{"input":[{"type":"reasoning","summary":[]}]}`, "input"},
		{"lost reference", `{"input":[{"type":"item_reference","id":"msg_old"}]}`, `{"input":[]}`, "input"},
		{"changed reference", `{"input":[{"type":"item_reference","id":"call_123"}]}`, `{"input":[{"type":"item_reference","id":"fc_456"}]}`, "input"},
		{"wrong call id", `{"input":[{"type":"function_call_output","call_id":"call_123","output":"answer"}]}`, `{"input":[{"type":"function_call_output","call_id":"fc_456","output":"answer"}]}`, "input"},
		{"changed result", `{"input":[{"type":"function_call_output","call_id":"call_123","output":"answer"}]}`, `{"input":[{"type":"function_call_output","call_id":"fc_123","output":"different"}]}`, "input"},
		{"lost tools", `{"tools":[{"type":"function","name":"lookup"}]}`, `{"tools":[]}`, "tools"},
		{"conflicting legacy tools", `{"tools":[{"type":"function","name":"important"}],"functions":[{"name":"legacy"}]}`, `{"tools":[{"type":"function","name":"legacy"}]}`, "tools"},
		{"tool without association", `{"input":[{"role":"tool","content":"answer"}]}`, `{"input":[{"role":"user","content":"answer"}]}`, "input"},
		{"lost malformed tool", `{"tools":[{"type":"function"}]}`, `{"tools":[]}`, "tools"},
		{"lowered reasoning", `{"reasoning":{"effort":"high"}}`, `{"reasoning":{"effort":"low"}}`, "reasoning"},
		{"lost Astra mode", `{"model":"gpt-6-astra","reasoning":{"mode":"pro"}}`, `{"model":"gpt-6-astra","reasoning":{"effort":"max"}}`, "reasoning"},
		{"changed model", `{"model":"gpt-5.4"}`, `{"model":"gpt-5.4-mini"}`, "model"},
		{"changed budget", `{"max_output_tokens":2000}`, `{"max_output_tokens":10}`, "max_output_tokens"},
		{"lost continuity", `{"previous_response_id":"resp_123"}`, `{}`, "previous_response_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, validateMode1RequestIntegrityForAccount(mode1TestAccount(1), []byte(tc.before), []byte(tc.after)), tc.field)
		})
	}
	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.ErrorContains(t, validateMode1RequestIntegrityForAccount(apiKey, []byte(`{"max_output_tokens":2000}`), []byte(`{}`)), "max_output_tokens")
}

func TestMode1GuardAcceptsActualCodexTransform(t *testing.T) {
	for _, raw := range []string{
		`{"model":"gpt-5.4","input":"Return OK","max_output_tokens":100}`,
		`{"model":"gpt-5.4","instructions":"Be accurate","input":[{"role":"system","content":"Use the context"},{"role":"user","content":"one"},{"role":"assistant","content":"two"},{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"},{"role":"user","content":"continue"}],"reasoning":{"effort":"high"}}`,
		`{"model":"gpt-5.4","input":[{"type":"function_call","call_id":"call_123","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_123","output":"answer"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`,
	} {
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &body))
		result := applyCodexOAuthTransformWithOptions(body, codexOAuthTransformOptions{OmitPromotedSystemMessagesFromInput: true})
		require.NoError(t, result.Error)
		forwarded, err := json.Marshal(body)
		require.NoError(t, err)
		require.NoError(t, validateMode1RequestIntegrityForAccount(mode1TestAccount(1), []byte(raw), forwarded), string(forwarded))
	}
}

func TestMode1GuardRejectsInvalidJSON(t *testing.T) {
	for _, raw := range []string{"null", "[]", "{} {}", "{", "{} trailing"} {
		require.Error(t, validateMode1RequestIntegrity([]byte(raw), []byte(`{}`)), raw)
		require.Error(t, validateMode1RequestIntegrity([]byte(`{}`), []byte(raw)), raw)
	}
}
