package service

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

type pelicanTestContextKey struct{}

type pelicanTestOptions struct {
	prompt          string
	reasoningEffort string
}

func withPelicanTestOptions(ctx context.Context, options pelicanTestOptions) context.Context {
	return context.WithValue(ctx, pelicanTestContextKey{}, options)
}

func pelicanTestOptionsFromContext(ctx context.Context) (pelicanTestOptions, bool) {
	options, ok := ctx.Value(pelicanTestContextKey{}).(pelicanTestOptions)
	return options, ok
}

// TestPelicanAccountConnection is the dedicated account test path for the
// Pelican UI. The existing /test endpoint deliberately keeps its historical
// probe payload; only this endpoint opts into the user prompt and reasoning.
func (s *AccountTestService) TestPelicanAccountConnection(c *gin.Context, accountID int64, modelID, prompt, reasoningEffort string) error {
	options := pelicanTestOptions{
		prompt:          strings.TrimSpace(prompt),
		reasoningEffort: normalizePelicanReasoningEffort(reasoningEffort),
	}
	ctx := withPelicanTestOptions(c.Request.Context(), options)
	c.Request = c.Request.WithContext(ctx)
	return s.TestAccountConnection(c, accountID, modelID, options.prompt, AccountTestModeDefault)
}

func normalizePelicanReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func createPelicanClaudePayload(modelID, prompt string) (map[string]any, error) {
	payload, err := createTestPayload(modelID)
	if err != nil {
		return nil, err
	}
	messages, ok := payload["messages"].([]map[string]any)
	if !ok || len(messages) == 0 {
		return payload, nil
	}
	content, ok := messages[0]["content"].([]map[string]any)
	if ok && len(content) > 0 {
		content[0]["text"] = promptOrDefault(prompt)
	}
	return payload, nil
}

func createPelicanOpenAIPayload(modelID string, isOAuth bool, prompt, reasoningEffort string) map[string]any {
	payload := createOpenAITestPayload(modelID, isOAuth)
	input, ok := payload["input"].([]map[string]any)
	if ok && len(input) > 0 {
		content, ok := input[0]["content"].([]map[string]any)
		if ok && len(content) > 0 {
			content[0]["text"] = promptOrDefault(prompt)
		}
	}
	if effort := normalizePelicanReasoningEffort(reasoningEffort); effort != "" {
		payload["reasoning"] = map[string]any{"effort": effort}
	}
	return payload
}

func promptOrDefault(prompt string) string {
	if value := strings.TrimSpace(prompt); value != "" {
		return value
	}
	return "hi"
}
