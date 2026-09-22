package repository

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestOpenAITurnAdmissionReadTransaction(t *testing.T) {
	for _, failure := range []string{"", "account", "groups", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			defer func() { _ = client.Close() }()
			repo := &accountRepository{client: client}
			mock.ExpectBegin()
			query := mock.ExpectQuery(`SELECT .* FROM "accounts"`)
			if failure == "account" {
				query.WillReturnError(errors.New("read failed"))
				mock.ExpectRollback()
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "status", "schedulable"}).
					AddRow(7, "openai", "oauth", "active", true))
				groups := mock.ExpectQuery(`SELECT .* FROM "account_groups"`)
				if failure == "groups" {
					groups.WillReturnError(errors.New("group read failed"))
					mock.ExpectRollback()
				} else {
					groups.WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id"}))
					commit := mock.ExpectCommit()
					if failure == "commit" {
						commit.WillReturnError(errors.New("commit failed"))
					}
				}
			}
			a, parent, err := repo.GetOpenAITurnAdmission(context.Background(), 7)
			if failure == "" {
				require.NoError(t, err)
				require.EqualValues(t, 7, a.ID)
				require.True(t, a.IsSchedulable())
				require.Empty(t, a.GroupIDs)
			} else {
				require.Error(t, err)
				require.Nil(t, a, "no stale/split snapshot fallback")
			}
			require.Nil(t, parent)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
