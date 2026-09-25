package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPelicanPayloadsDoNotChangeDefaultAccountTestPayloads(t *testing.T) {
	defaultClaude, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	defaultMessages, ok := defaultClaude["messages"].([]map[string]any)
	require.True(t, ok)
	defaultContent, ok := defaultMessages[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, "hi", defaultContent[0]["text"])

	defaultOpenAI := createOpenAITestPayload("gpt-6-astra", true)
	defaultInput, ok := defaultOpenAI["input"].([]map[string]any)
	require.True(t, ok)
	defaultOpenAIContent, ok := defaultInput[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, "hi", defaultOpenAIContent[0]["text"])
	require.NotContains(t, defaultOpenAI, "reasoning")

	pelicanClaude, err := createPelicanClaudePayload("claude-sonnet-4-6", "draw the pelican animation")
	require.NoError(t, err)
	pelicanMessages, ok := pelicanClaude["messages"].([]map[string]any)
	require.True(t, ok)
	pelicanContent, ok := pelicanMessages[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, "draw the pelican animation", pelicanContent[0]["text"])

	pelicanOpenAI := createPelicanOpenAIPayload("gpt-6-astra", true, "draw the pelican animation", "medium")
	pelicanInput, ok := pelicanOpenAI["input"].([]map[string]any)
	require.True(t, ok)
	pelicanOpenAIContent, ok := pelicanInput[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, "draw the pelican animation", pelicanOpenAIContent[0]["text"])
	require.Equal(t, map[string]any{"effort": "medium"}, pelicanOpenAI["reasoning"])
}
