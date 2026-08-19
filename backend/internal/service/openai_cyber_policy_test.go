package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMarkAndGetOpsCyberPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	require.Nil(t, GetOpsCyberPolicy(c), "no mark initially")

	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:           "cyber_policy",
		Message:        "This request was flagged for cyber policy.",
		Body:           `{"error":{"code":"cyber_policy"}}`,
		UpstreamStatus: 400,
	})

	got := GetOpsCyberPolicy(c)
	require.NotNil(t, got)
	require.Equal(t, "cyber_policy", got.Code)
	require.Equal(t, 400, got.UpstreamStatus)
}

func TestMarkOpsCyberPolicyFirstWins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "first"})
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "second"})
	require.Equal(t, "first", GetOpsCyberPolicy(c).Message, "first mark wins, later marks ignored")
}

func TestMarkOpsCyberPolicyPreservesUsagePolicySessionBlockCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:    OpenAIUsagePolicySessionBlockCode,
		Message: "explicit upstream usage-policy rejection",
	})

	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, OpenAIUsagePolicySessionBlockCode, mark.Code)
}

func TestMarkOpsCyberPolicyNilContext(t *testing.T) {
	MarkOpsCyberPolicy(nil, CyberPolicyMark{Code: "cyber_policy"})
	require.Nil(t, GetOpsCyberPolicy(nil))
}

// TestClearOpsCyberPolicy_AllowsRemark verifies F1: after Clear, Get returns nil
// and a subsequent Mark takes effect (per-turn lifecycle in WS connections).
func TestClearOpsCyberPolicy_AllowsRemark(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	MarkOpsCyberPolicy(c, CyberPolicyMark{Message: "first", UpstreamStatus: 200})
	require.NotNil(t, GetOpsCyberPolicy(c))

	ClearOpsCyberPolicy(c)
	require.Nil(t, GetOpsCyberPolicy(c), "mark must be invisible after Clear")

	MarkOpsCyberPolicy(c, CyberPolicyMark{Message: "second", UpstreamStatus: 400})
	got := GetOpsCyberPolicy(c)
	require.NotNil(t, got, "re-mark after Clear must take effect")
	require.Equal(t, "second", got.Message)
}

func TestDetectOpenAICyberPolicy(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		hit     bool
		code    string
		msg     string
	}{
		{"top-level error", `{"error":{"code":"cyber_policy","message":"flagged"}}`, true, "cyber_policy", "flagged"},
		{"response-wrapped", `{"response":{"error":{"code":"cyber_policy","message":"  bad  "}}}`, true, "cyber_policy", "bad"},
		{"case-insensitive", `{"error":{"code":"Cyber_Policy"}}`, true, "cyber_policy", ""},
		{"explicit policy rejection message", `{"error":{"message":"Invalid prompt: your prompt was flagged as potentially violating our usage policy. Please try again with a different prompt"}}`, true, OpenAIUsagePolicySessionBlockCode, "Invalid prompt: your prompt was flagged as potentially violating our usage policy. Please try again with a different prompt"},
		{"response-wrapped policy rejection message", `{"response":{"error":{"message":"INVALID PROMPT: YOUR PROMPT WAS FLAGGED AS POTENTIALLY VIOLATING OUR USAGE POLICY"}}}`, true, OpenAIUsagePolicySessionBlockCode, "INVALID PROMPT: YOUR PROMPT WAS FLAGGED AS POTENTIALLY VIOLATING OUR USAGE POLICY"},
		{"ordinary overload remains retryable", `{"error":{"message":"Our servers are currently overloaded. Please try again later."}}`, false, "", ""},
		{"vague safety text is not enough", `{"error":{"message":"Request could not be processed due to a policy check"}}`, false, "", ""},
		{"content_policy not cyber", `{"error":{"code":"content_policy","message":"x"}}`, false, "", ""},
		{"safety message not cyber", `{"error":{"type":"safety_error","message":"high-risk cyber activity"}}`, false, "", ""},
		{"empty", ``, false, "", ""},
		{"upstream_error", `{"error":{"code":"upstream_error"}}`, false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, code, msg := detectOpenAICyberPolicy([]byte(tc.payload))
			require.Equal(t, tc.hit, hit)
			if tc.hit {
				require.Equal(t, tc.code, code)
				require.Equal(t, tc.msg, msg)
			}
		})
	}
}
