package repository

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type downstreamCookieRow struct{ debug string }

func (row downstreamCookieRow) Scan(dest ...any) error {
	*(dest[len(dest)-3].(*sql.NullString)) = sql.NullString{String: row.debug, Valid: true}
	return nil
}

func TestUsageLogDownstreamCookiesStoredSeparately(t *testing.T) {
	incoming := "route=client; other=one"
	outgoing := "route=client\nother=one"
	upstream := "route=internal"
	prepared := prepareUsageLogInsert(&service.UsageLog{
		DownstreamRequestCookie: &incoming, DownstreamResponseCookie: &outgoing,
		RequestCookie: &upstream, ResponseCookie: &upstream,
	})
	var debug map[string]string
	require.NoError(t, json.Unmarshal([]byte(prepared.args[len(prepared.args)-3].(string)), &debug))
	require.Equal(t, incoming, debug["downstream_request_cookie"])
	require.Equal(t, outgoing, debug["downstream_response_cookie"])
	require.Equal(t, upstream, debug["request_cookie"])
	require.Equal(t, upstream, debug["response_cookie"])
	loaded, err := scanUsageLog(downstreamCookieRow{debug: prepared.args[len(prepared.args)-3].(string)})
	require.NoError(t, err)
	require.Equal(t, &incoming, loaded.DownstreamRequestCookie)
	require.Equal(t, &outgoing, loaded.DownstreamResponseCookie)
	require.Equal(t, &upstream, loaded.RequestCookie)
	require.Equal(t, &upstream, loaded.ResponseCookie)
	legacy, err := scanUsageLog(downstreamCookieRow{debug: `{}`})
	require.NoError(t, err)
	require.Nil(t, legacy.DownstreamRequestCookie)
	require.Nil(t, legacy.DownstreamResponseCookie)
}
