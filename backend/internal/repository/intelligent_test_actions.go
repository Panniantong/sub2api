package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *intelligentTestRepository) DeferForCapacity(ctx context.Context, record *service.IntelligentTestRecord) error {
	until := time.Now().Add(5 * time.Second)
	if record.AvailableAt != nil && record.AvailableAt.After(until) {
		until = *record.AvailableAt
	}
	reason := record.QueueReason
	if reason == "" {
		reason = "等待账号空闲，不占用额外业务并发"
	}
	result, err := r.db.ExecContext(ctx, `UPDATE account_tests SET status='queued',queue_reason=$3,available_at=$4,started_at=NULL,lease_token=NULL,lease_until=NULL WHERE id=$1 AND status='running' AND lease_token=$2`, record.ID, record.LeaseToken, reason, until)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return fmt.Errorf("test %d lost execution lease before deferral", record.ID)
	}
	return err
}

func (r *intelligentTestRepository) Cancel(ctx context.Context, actor, id int64) (*service.IntelligentTestRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := scanIntelligentRecord(tx.QueryRowContext(ctx, `SELECT `+intelligentRecordColumns+` FROM account_tests t WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if record.Status == "cancelled" {
		return record, nil
	}
	if record.Status != "queued" {
		return nil, infraerrors.Conflict("INTELLIGENT_TEST_NOT_QUEUED", "只能取消尚未执行的排队任务")
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_tests SET status='cancelled',queue_reason='',error_message='管理员取消了排队任务',finished_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	if err = insertIntelligentAudit(ctx, tx, actor, "admin.intelligent_tests.cancel", map[string]any{"record_id": id}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	record.Status = "cancelled"
	record.ErrorMessage = "管理员取消了排队任务"
	now := time.Now()
	record.FinishedAt = &now
	record.QueueReason = ""
	return record, nil
}

func (r *intelligentTestRepository) Reevaluate(ctx context.Context, actor, id int64, evaluate func(*service.IntelligentTestRecord) error) (*service.IntelligentTestRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := scanIntelligentRecord(tx.QueryRowContext(ctx, `SELECT `+intelligentRecordColumns+` FROM account_tests t WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if service.IsCurrentIntelligentAssessment(record.Evaluation) {
		return record, nil
	}
	before := map[string]any{"status": record.Status, "score": record.Score, "evaluation": record.Evaluation}
	if err = evaluate(record); err != nil {
		return nil, err
	}
	if _, exists := record.Evaluation["original_judgment"]; !exists {
		record.Evaluation["original_judgment"] = before
	}
	record.Evaluation["reevaluated_at"] = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(record.Evaluation)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_tests SET status=$2,score=$3,result_image=$4,evaluation=$5::jsonb WHERE id=$1`, id, record.Status, record.Score, record.ResultImage, string(data))
	if err != nil {
		return nil, err
	}
	if err = insertIntelligentAudit(ctx, tx, actor, "admin.intelligent_tests.reevaluate", map[string]any{"record_id": id, "previous": before, "evaluator_version": service.IntelligentEvaluatorVersion}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return record, nil
}
