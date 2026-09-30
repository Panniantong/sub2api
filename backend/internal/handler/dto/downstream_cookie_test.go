package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogDownstreamCookiesAreAdminOnly(t *testing.T) {
	incoming, outgoing := "route=client; other=one", "route=client\nother=one"
	log := &service.UsageLog{DownstreamRequestCookie: &incoming, DownstreamResponseCookie: &outgoing}
	admin, err := json.Marshal(UsageLogFromServiceAdmin(log))
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(admin, &payload))
	require.Equal(t, incoming, payload["downstream_request_cookie"])
	require.Equal(t, outgoing, payload["downstream_response_cookie"])
	user, err := json.Marshal(UsageLogFromService(log))
	require.NoError(t, err)
	require.NotContains(t, string(user), "downstream_request_cookie")
	require.NotContains(t, string(user), "downstream_response_cookie")
}
