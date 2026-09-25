package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanClaimUsesDatabaseLeaseAndSavedVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewScheduledTestPlanRepository(db)
	now := time.Now()
	until := now.Add(15 * time.Minute)
	next := now.Add(30 * time.Minute)
	plan := &service.ScheduledTestPlan{ID: 1, UpdatedAt: now.Add(-time.Hour)}
	query := `(?s)UPDATE scheduled_test_plans.*enabled = true.*running_until IS NULL.*updated_at = \$5.*deleted_at IS NULL`
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(plan.AccountID).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(query).WithArgs(plan.ID, now, until, next, plan.UpdatedAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	ok, err := repo.ClaimPelican(context.Background(), plan, now, until, next)
	require.NoError(t, err)
	require.True(t, ok)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(plan.AccountID).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(query).WithArgs(plan.ID, now, until, next, plan.UpdatedAt).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	ok, err = repo.ClaimPelican(context.Background(), plan, now, until, next)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestPelicanCleanupIncludesPausedPlansAndBoundsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	mock.ExpectExec(`(?s)DELETE FROM scheduled_test_results.*WHERE plans.pelican_config IS NOT NULL AND results.created_at < \$1\s+ORDER BY results.created_at LIMIT 1000`).WithArgs(cutoff).WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, NewScheduledTestResultRepository(db).PruneExpiredPelican(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}
