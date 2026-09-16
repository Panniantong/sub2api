package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountProxyModeCheckMigration(t *testing.T) {
	content, err := FS.ReadFile("235_account_proxy_mode_check.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "chk_accounts_extra_proxy_mode")
	require.Contains(t, sql, "lower(btrim(extra->>'proxy_mode')) = 'random'")
	require.Contains(t, sql, "VALIDATE CONSTRAINT chk_accounts_extra_proxy_mode")
}
