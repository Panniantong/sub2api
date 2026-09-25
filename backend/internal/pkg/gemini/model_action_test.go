package gemini

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseModelAction(t *testing.T) {
	for _, tc := range []struct {
		in, model, action string
		ok                bool
	}{
		{in: "gemini-2.5-pro:generateContent", model: "gemini-2.5-pro", action: ActionGenerateContent, ok: true},
		{in: " gemini-2.5-pro:streamGenerateContent ", model: "gemini-2.5-pro", action: ActionStreamGenerateContent, ok: true},
		{in: "gemini-2.5-pro/countTokens", model: "gemini-2.5-pro", action: ActionCountTokens, ok: true},
		// 与 handler 一直以来的行为一致：取第一个冒号
		{in: "a:b:generateContent", model: "a", action: "b:generateContent", ok: true},
		{in: "", ok: false},
		{in: ":generateContent", ok: false},
		{in: "gemini-2.5-pro:", ok: false},
		{in: "gemini-2.5-pro", ok: false},
	} {
		model, action, ok := ParseModelAction(tc.in)
		require.Equal(t, tc.ok, ok, tc.in)
		require.Equal(t, tc.model, model, tc.in)
		require.Equal(t, tc.action, action, tc.in)
	}
}
