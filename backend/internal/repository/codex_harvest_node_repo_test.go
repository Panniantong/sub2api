package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHarvestFeedbackRejectsOldGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT generation.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(2))
	mock.ExpectRollback()
	stored, err := NewCodexHarvestNodeRepository(db).Record(context.Background(), service.CodexHarvestNodeFeedback{Generation: 1, Result: "success"})
	require.NoError(t, err)
	require.False(t, stored)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHarvestResetEpochAndRowsAreAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE codex_harvest_learning_epoch").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM codex_harvest_nodes").WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, NewCodexHarvestNodeRepository(db).Reset(context.Background(), 17))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHarvestCancelledDoesNotPenalizeNode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	stored, err := NewCodexHarvestNodeRepository(db).Record(context.Background(), service.CodexHarvestNodeFeedback{Result: "cancelled"})
	require.NoError(t, err)
	require.False(t, stored)
	require.NoError(t, mock.ExpectationsWereMet())
}
