package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupMapperExposesStreamOnlyToAdminsOnly(t *testing.T) {
	group := &service.Group{ID: 3, Name: "cc", Platform: service.PlatformAnthropic, StreamOnly: true}

	admin, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(admin), `"stream_only":true`)

	user, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(user), "stream_only")
}
