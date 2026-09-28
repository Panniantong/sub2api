package repository

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCookieHostMonitorRejectsAllHostWriteEntrypoints(t *testing.T) {
	for _, operation := range []string{"extra", "bulk", "full"} {
		t.Run(operation, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`SELECT .*settings.*`).WillReturnRows(sqlmock.NewRows([]string{"id", "key", "value"}).AddRow(1, "openai_cookie_settings", `{"host_monitor":{"enabled":true,"account_id":21,"host":"fixed.example"}}`))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			extra := map[string]any{"codex_cookie_host": nil}
			switch operation {
			case "extra":
				err = repo.UpdateExtra(context.Background(), 21, extra)
			case "bulk":
				_, err = repo.BulkUpdate(context.Background(), []int64{21}, service.AccountBulkUpdate{Extra: extra})
			case "full":
				err = repo.Update(context.Background(), &service.Account{ID: 21, Extra: extra})
			}
			require.ErrorContains(t, err, "先关闭监控")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
