package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

func TestTimingWriteJoinsBillingIdentityAndKeepsDistinctTraces(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	for _, traceID := range []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"} {
		mock.ExpectExec("INSERT INTO request_timing_details").WithArgs(traceID, sqlmock.AnyArg(), "client:repeat", int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
		if err := repo.writeTiming(context.Background(), timingWrite{requestID: "client:repeat", apiKeyID: 8, data: requesttiming.Snapshot{TraceID: traceID}}); err != nil {
			t.Fatal(err)
		}
	}
	mock.ExpectQuery("SELECT detail FROM request_timing_details WHERE usage_log_id").WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"detail"}).AddRow(`{"trace_id":"one"}`).AddRow(`{"trace_id":"two"}`))
	got, err := repo.RequestTimings(context.Background(), 9)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestTimingBindingDoesNotWriteUntilRequestFinished(t *testing.T) {
	// A manually supplied queue exercises lifecycle without leaking a worker.
	repo := &usageLogRepository{timingQueue: make(chan timingWrite, 1)}
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	repo.db = db
	repo.timingOnce.Do(func() {})
	c := requesttiming.New(time.Now(), 0)
	ctx := requesttiming.With(context.Background(), c)
	repo.RecordRequestTiming(ctx, "client:r", 3)
	select {
	case <-repo.timingQueue:
		t.Fatal("premature snapshot")
	default:
	}
	c.Finish(200, false)
	select {
	case job := <-repo.timingQueue:
		raw, err := json.Marshal(job.data)
		if err != nil || len(raw) == 0 || job.data.Status != 200 {
			t.Fatal("invalid final snapshot")
		}
	default:
		t.Fatal("missing final snapshot")
	}
}
