package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIntelligentAssessmentQueueActionsAndHistory(t *testing.T) {
	db := intelligentTestDB(t)
	repo := &intelligentTestRepository{db: db}
	svc := service.NewIntelligentTestService(repo, nil) // no runner: re-evaluation cannot use upstream
	ctx := context.Background()
	enqueue := func(model string) (*service.IntelligentTestEnqueued, error) {
		return svc.Enqueue(ctx, 1, service.IntelligentTestEnqueue{AccountIDs: []int64{10}, TestTypes: []string{"candy"}, Models: map[string]string{"candy": model}, IdempotencyKey: uuid.NewString()})
	}
	first, err := enqueue("gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, 1, first.CreatedCount)
	again, err := enqueue("gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, 1, again.ReusedCount)
	require.Zero(t, again.CreatedCount)
	_, err = enqueue("gpt-5.4-mini")
	require.ErrorContains(t, err, "不同模型或配置")
	_, err = svc.Cancel(ctx, 2, first.Records[0].ID)
	require.ErrorIs(t, err, service.ErrIntelligentTestForbidden)
	running, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, running)
	_, err = svc.Cancel(ctx, 1, running.ID)
	require.ErrorContains(t, err, "只能取消")
	require.NoError(t, repo.DeferForCapacity(ctx, running))
	waiting, err := repo.Get(ctx, running.ID)
	require.NoError(t, err)
	require.Equal(t, "queued", waiting.Status)
	require.Contains(t, waiting.QueueReason, "等待账号空闲")
	none, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, none, "busy tasks do not monopolize claim cycles")
	cancelled, err := svc.Cancel(ctx, 1, running.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
	_, err = svc.Cancel(ctx, 1, running.ID)
	require.NoError(t, err)
	var audits int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='admin.intelligent_tests.cancel'`).Scan(&audits))
	require.Equal(t, 1, audits)

	// Construct a complete historical judgment with its original private snapshot.
	cfg := service.IntelligentTestConfig{Prompt: "original question", Model: "original-model", Evaluator: "exact_answer", ExpectedAnswer: "12", TimeoutSeconds: 120}
	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	var oldID int64
	require.NoError(t, db.QueryRow(`INSERT INTO account_tests(account_id,test_type,status,score,result,input,raw_response,config_snapshot,evaluation,finished_at,anti_degradation,requested_by) VALUES(10,'candy','suspected_degradation',0,'12','original question','original raw',$1,'{"method":"exact_answer","reason":"legacy strict mismatch"}',NOW(),true,1) RETURNING id`, string(encoded)).Scan(&oldID))
	// Current settings differ: re-evaluation must use the historical snapshot.
	_, err = db.Exec(`UPDATE test_settings SET config=jsonb_set(config,'{expected_answer}','"999"'),user_visible=true WHERE test_type='candy'`)
	require.NoError(t, err)
	next, err := enqueue("gpt-5.4-mini")
	require.NoError(t, err)
	f := service.IntelligentTestFilter{AccountID: 10, TestType: "candy", Page: 1, PageSize: 12, OnlyAbnormal: true}
	before, err := repo.Accounts(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 1, before.Total)
	require.EqualValues(t, 1, before.Overview.ReviewAccounts)
	require.Zero(t, before.Overview.AbnormalAccounts)
	require.Equal(t, next.Records[0].ID, before.Items[0].Tests[0].Latest.ID)
	require.Equal(t, oldID, before.Items[0].Tests[0].LatestCompleted.ID)
	require.Empty(t, before.Items[0].Tests[0].LatestCompleted.RawResponse)
	require.Empty(t, before.Items[0].Tests[0].Risk)
	_, err = svc.Reevaluate(ctx, 2, oldID)
	require.ErrorIs(t, err, service.ErrIntelligentTestForbidden)
	updated, err := svc.Reevaluate(ctx, 1, oldID)
	require.NoError(t, err)
	require.Equal(t, "completed", updated.Status)
	require.Equal(t, "correct", updated.Evaluation["answer_verdict"])
	require.Equal(t, "non_compliant", updated.Evaluation["format_verdict"])
	require.Equal(t, 100.0, *updated.Score)
	require.Equal(t, "12", updated.Result)
	require.Equal(t, "original raw", updated.RawResponse)
	require.Equal(t, "12", updated.ConfigSnapshot.ExpectedAnswer)
	require.Contains(t, updated.Evaluation, "original_judgment")
	_, err = svc.Reevaluate(ctx, 1, oldID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='admin.intelligent_tests.reevaluate'`).Scan(&audits))
	require.Equal(t, 1, audits)
	public, err := repo.PublicGet(ctx, 2, oldID)
	require.NoError(t, err)
	require.Equal(t, "correct", public.Evaluation["answer_verdict"])
	require.NotContains(t, public.Evaluation, "expected_answer")
	require.NotContains(t, public.Evaluation, "original_judgment")
	after, err := repo.Accounts(ctx, f)
	require.NoError(t, err)
	require.Zero(t, after.Total)
	f.OnlyAbnormal = false
	after, err = repo.Accounts(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 1, after.Overview.SuccessAccounts)
	require.Zero(t, after.Overview.ReviewAccounts)
	// Cancelled attempts also retain the last completed result.
	_, err = svc.Cancel(ctx, 1, next.Records[0].ID)
	require.NoError(t, err)
	after, err = repo.Accounts(ctx, f)
	require.NoError(t, err)
	require.Equal(t, oldID, after.Items[0].Tests[0].LatestCompleted.ID)
	require.EqualValues(t, 1, after.Overview.SuccessAccounts)
}

func TestIntelligentQueueDefersUntilCooldownAndAllowsCancellation(t *testing.T) {
	db := intelligentTestDB(t)
	repo := &intelligentTestRepository{db: db}
	ctx := context.Background()
	_, err := repo.Enqueue(ctx, 1, service.IntelligentTestEnqueue{AccountIDs: []int64{10}, TestTypes: []string{"candy"}, IdempotencyKey: uuid.NewString()})
	require.NoError(t, err)
	r, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, r)
	until := time.Now().Add(2 * time.Minute).UTC().Truncate(time.Second)
	r.AvailableAt = &until
	r.QueueReason = "模型冷却，等待后执行"
	require.NoError(t, repo.DeferForCapacity(ctx, r))
	saved, err := repo.Get(ctx, r.ID)
	require.NoError(t, err)
	require.True(t, until.Equal(*saved.AvailableAt), "queue timing is an instant, independent of database timezone")
	require.Equal(t, r.QueueReason, saved.QueueReason)
	next, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Error(t, repo.DeferForCapacity(ctx, r), "an expired worker cannot alter a queued/cancelled job")
	cancelled, err := repo.Cancel(ctx, 1, r.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
}

func TestIntelligentDeferredTaskDoesNotStarveAlreadyWaitingTask(t *testing.T) {
	db := intelligentTestDB(t)
	repo := &intelligentTestRepository{db: db}
	ctx := context.Background()
	_, err := repo.Enqueue(ctx, 1, service.IntelligentTestEnqueue{AccountIDs: []int64{10, 11}, TestTypes: []string{"candy"}, IdempotencyKey: uuid.NewString()})
	require.NoError(t, err)
	first, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 10, first.AccountID)
	require.NoError(t, repo.DeferForCapacity(ctx, first))
	_, err = db.Exec(`UPDATE account_tests SET available_at=CASE WHEN id=$1 THEN NOW()-INTERVAL '1 second' ELSE NOW()-INTERVAL '1 minute' END WHERE status='queued'`, first.ID)
	require.NoError(t, err)
	next, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 11, next.AccountID, "a retried low-ID task must yield to an older waiting task")
}
