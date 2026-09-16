package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntelligentAssessmentSeparatesAnswerAndFormat(t *testing.T) {
	cfg := IntelligentTestConfig{ExpectedAnswer: "12", AnswerType: "number", AnswerUnit: "颗"}
	for _, output := range []string{"12", "最后盒子里有12颗糖。", "ANSWER: 12颗", "**ANSWER: 12**", "```text\nANSWER: 12\n```", "ANSWER: 12\n这是最终数量。", "ANSWER: 12\nANSWER: 12", "ANSWER: 12.0", "ANSWER: １２", `{"answer":12}`, "ANSWER: 24/2", "ANSWER: 1.2e1"} {
		t.Run(output, func(t *testing.T) {
			result := evaluateIntelligentAnswer(output, cfg)
			require.Equal(t, "completed", result.Status)
			require.Equal(t, "correct", result.Detail["answer_verdict"], result.Detail)
			require.Equal(t, 100.0, *result.Score)
			require.Equal(t, "insufficient_evidence", result.Detail["capability_verdict"])
		})
	}
	require.Equal(t, "non_compliant", evaluateIntelligentAnswer("12", cfg).Detail["format_verdict"])
	require.Equal(t, "compliant", evaluateIntelligentAnswer("ANSWER: 12颗", cfg).Detail["format_verdict"])
	cfg.AnswerFormat = "free_text"
	require.Equal(t, "not_required", evaluateIntelligentAnswer("12", cfg).Detail["format_verdict"])
	for _, output := range []string{"ANSWER: 12\nANSWER: 11", "ANSWER: 12\n11", `{"answer":12,"final_answer":11}`, "计算中出现12，但无法确定最终答案。", "ANSWER: 12米", "ANSWER: 1e999999", "ANSWER: 1,2", "ANSWER: 12或13", ""} {
		result := evaluateIntelligentAnswer(output, cfg)
		require.Equal(t, "undetermined", result.Detail["answer_verdict"], output)
		require.Nil(t, result.Score)
	}
	wrong := evaluateIntelligentAnswer("中间值是12\nANSWER: 11", cfg)
	require.Equal(t, "incorrect", wrong.Detail["answer_verdict"])
	require.Equal(t, 0.0, *wrong.Score)
	reasoning := evaluateIntelligentAnswer("错误推导: 2+2=12\nANSWER: 12", cfg)
	require.Equal(t, "correct", reasoning.Detail["answer_verdict"])
	require.Contains(t, reasoning.Detail["limitation"], "未验证完整推导")
	cfg.ExpectedAnswer = "1,200"
	require.Equal(t, "correct", evaluateIntelligentAnswer("1200", cfg).Detail["answer_verdict"])
	cfg.AnswerType, cfg.ExpectedAnswer = "text", "012"
	require.Equal(t, "incorrect", evaluateIntelligentAnswer("12", cfg).Detail["answer_verdict"])
}

func TestIntelligentAssessmentRejectsNegatedAndDuplicateConflicts(t *testing.T) {
	cfg := IntelligentTestConfig{ExpectedAnswer: "12", AnswerType: "number", AnswerUnit: "颗"}
	for _, output := range []string{"因此答案不是12", "最后盒子里没有12颗糖", "因此假设答案是12", "最终答案可能是12", "因此答案至少为12", "> ANSWER: 12", `{"answer":11,"answer":12}`, `{"answer":12,"answer":11}`} {
		result := evaluateIntelligentAnswer(output, cfg)
		require.Equal(t, "undetermined", result.Detail["answer_verdict"], output)
		require.Nil(t, result.Score, output)
	}
	require.Equal(t, "correct", evaluateIntelligentAnswer(`{"answer":12,"answer":12}`, cfg).Detail["answer_verdict"])
	require.Equal(t, "correct", evaluateIntelligentAnswer("最后盒子里有12颗糖。", cfg).Detail["answer_verdict"])
}

func TestIntelligentAssessmentUnitModesAreExplicit(t *testing.T) {
	cfg := IntelligentTestConfig{Prompt: "盒子里有 24 颗糖。小明取走总数的四分之一", ExpectedAnswer: "12", Evaluator: "exact_answer", TimeoutSeconds: 120}
	require.Equal(t, "correct", evaluateIntelligentAnswer("12颗", cfg).Detail["answer_verdict"], "old snapshots retain compatibility")
	cfg.AnswerUnitMode = "none"
	require.Equal(t, "undetermined", evaluateIntelligentAnswer("12颗", cfg).Detail["answer_verdict"])
	require.Equal(t, "correct", evaluateIntelligentAnswer("12", cfg).Detail["answer_verdict"])
	cfg.AnswerUnitMode = "configured"
	require.Error(t, validateIntelligentTestConfig(&cfg, DefaultIntelligentTestEvaluators()))
	cfg.AnswerUnit = "颗"
	require.NoError(t, validateIntelligentTestConfig(&cfg, DefaultIntelligentTestEvaluators()))
	require.Equal(t, "correct", evaluateIntelligentAnswer("12颗糖", cfg).Detail["answer_verdict"])
	require.Equal(t, 3, evaluateIntelligentAnswer("12", cfg).Detail["evaluator_version"])
	require.NotNil(t, PublicIntelligentAssessment(map[string]any{"evaluator_version": 2, "answer_verdict": "correct"}))
	require.False(t, IsCurrentIntelligentAssessment(map[string]any{"evaluator_version": 2}))
}

func TestIntelligentSVGPreviewPreservesStaticDrawingAndRejectsEmpty(t *testing.T) {
	raw := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100"><style>.bird{fill:#fff;stroke:#123;stroke-width:2} #eye{fill:black} circle{fill:red}</style><defs><g id="wheel"><circle r="12" fill="none" stroke="black"/></g></defs><use href="#wheel" x="30" y="75"/><use href="#wheel" x="130" y="75"/><path class="bird" d="M50 40 Q70 5 100 40 L130 45 L100 50 Q60 80 50 40Z"/><circle id="eye" cx="91" cy="30" r="2"/><text x="10" y="95">A<tspan>B</tspan>C</text></svg>`
	safe, adjusted, err := PrepareIntelligentSVGPreview(raw)
	require.NoError(t, err)
	require.True(t, adjusted)
	require.Contains(t, safe, `translate(30 75)`)
	require.Contains(t, safe, `fill="#fff"`)
	require.Contains(t, safe, `id="eye" cx="91" cy="30" r="2" fill="black"`)
	require.Contains(t, safe, `>A<tspan>B</tspan>C</text>`)
	require.NotContains(t, safe, "<style")
	require.NotContains(t, safe, "<use")
	_, err = SanitizeIntelligentTestSVG(safe)
	require.NoError(t, err)
	result := evaluateIntelligentSVG(raw)
	require.Equal(t, "completed", result.Status)
	require.Nil(t, result.Score)
	require.NotEmpty(t, result.Image)
	for _, empty := range []string{`<svg><rect/></svg>`, `<svg><rect width="0" height="20"/></svg>`, `<svg><defs><circle r="5"/></defs></svg>`, `<svg><g opacity="0.0"><circle r="4"/></g></svg>`, `<svg><use href="#missing"/></svg>`, `<svg><g id="cycle"><use href="#cycle"/></g></svg>`, `<svg><circle></svg>`} {
		result := evaluateIntelligentSVG(empty)
		require.Empty(t, result.Image, empty)
		require.Nil(t, result.Score)
		require.Equal(t, "unavailable", result.Detail["image_state"])
	}
}

func TestIntelligentSVGPreviewStripsActiveAndExternalContent(t *testing.T) {
	for _, payload := range []string{
		`<script>alert(1)</script>`, `<foreignObject><style>circle{fill:red}</style><div>unsafe</div></foreignObject>`,
		`<image href="https://private.invalid/secret"/>`, `<use href="https://private.invalid/a.svg#x"/>`,
		`<animate attributeName="href" values="javascript:alert(1)"/>`,
		`<style>circle{fill:url(https://private.invalid/a)}</style>`, `<style>@import 'https://private.invalid/a';</style>`,
	} {
		safe, _, err := PrepareIntelligentSVGPreview(`<svg onload="alert(1)">` + payload + `<circle r="5" fill="blue" style="stroke:url(javascript:alert(1))"/></svg>`)
		require.NoError(t, err, payload)
		require.NotRegexp(t, `(?i)onload|script|foreignObject|<image|<use|<style|https:|<animate`, safe)
		require.Contains(t, safe, `fill="blue"`)
	}
	_, _, err := PrepareIntelligentSVGPreview(`<svg><!DOCTYPE x><circle r="2"/></svg>`)
	require.Error(t, err)
}

func TestIntelligentTerminalOnlyOutputAndErrorCategories(t *testing.T) {
	for _, delta := range []bool{false, true} {
		svc, _ := intelligentRunnerFixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if delta {
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ANSWER: 12\"}\n\n")
			}
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ANSWER: 12\"}]}]}}\n\n")
		})
		record := &IntelligentTestRecord{AccountID: 81, Input: "test", ConfigSnapshot: &IntelligentTestConfig{Model: "gpt-5.4"}}
		require.NoError(t, svc.RunIntelligentTest(context.Background(), record))
		require.Equal(t, "ANSWER: 12", strings.TrimSpace(record.Result))
		require.Equal(t, "gpt-5.4", record.ConfigSnapshot.Model)
		require.Equal(t, "gpt-5.4", record.ConfigSnapshot.Execution.RequestedModel)
	}
	for _, tt := range []struct {
		code          int
		message, want string
	}{
		{400, "max_output_tokens must be positive", "request_error"}, {400, "max_tokens exceeds request limit", "request_error"},
		{401, "max_tokens invalid", "account_error"}, {429, "expired token", "rate_limited"}, {503, "unavailable", "network_error"},
		{404, "model missing", "model_error"}, {0, "expired token", "account_error"}, {0, "connection refused", "network_error"},
	} {
		require.Equal(t, tt.want, classifyIntelligentError(tt.code, tt.message))
	}
}

func TestIntelligentAssessmentPublicProjection(t *testing.T) {
	detail := evaluateIntelligentAnswer("ANSWER: 12", IntelligentTestConfig{ExpectedAnswer: "12"}).Detail
	detail["original_judgment"] = map[string]any{"config": "private"}
	public := PublicIntelligentAssessment(detail)
	require.Equal(t, "correct", public["answer_verdict"])
	for _, private := range []string{"expected_answer", "actual_answer", "original_judgment", "reason", "candidate_answers"} {
		require.NotContains(t, public, private)
	}
	require.Nil(t, PublicIntelligentAssessment(map[string]any{"reason": "legacy private"}))
}

func TestProtectedRandomProxyRequestPreservesFixedProxy(t *testing.T) {
	proxyID, clear := int64(9), int64(0)
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 4, ProxyID: &proxyID}
	PrepareNewAccountProtection(a)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: a}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{ProxyID: &clear, Extra: map[string]any{"proxy_mode": "random"}})
	require.ErrorIs(t, err, ErrProtectedProxyModeChange)
	require.EqualValues(t, 9, *a.ProxyID)
	require.Equal(t, "legacy", a.ProtectionMode())
	require.False(t, a.IsRandomProxy())
}
