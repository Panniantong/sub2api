package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCodexProbeCompletion(t *testing.T) {
	for _, body := range []string{`{"status":"completed"}`, "data: {\"type\": \"response.completed\",\n" + "data: \"response\": {\"status\": \"completed\"}}\n\n", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\r\n\r\ndata: [DONE]\r\n\r\n"} {
		require.NoError(t, validateCodexProbeResponse([]byte(body)), body)
	}
	for _, body := range []string{"", "data: [DONE]\n\n", "data: {\"type\": \"error\"}\n\n", "data: {\"type\": \"response.created\"}\n\n", `{"status":"incomplete"}`, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\ndata: {\"type\":\"error\"}\n\n"} {
		require.Error(t, validateCodexProbeResponse([]byte(body)), body)
	}
}
