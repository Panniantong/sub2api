package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

func (r *intelligentTestRepository) Enqueue(ctx context.Context, actor int64, req service.IntelligentTestEnqueue) (*service.IntelligentTestEnqueued, error) {
	accountIDs := append([]int64{}, req.AccountIDs...)
	types := append([]string{}, req.TestTypes...)
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	sort.Strings(types)
	payload, _ := json.Marshal(struct {
		Accounts []int64
		Types    []string
		Models   map[string]string
	}{accountIDs, types, req.Models})
	sum := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(sum[:])
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Short transaction lock coordinates enqueue/capacity checks across replicas.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(247000)`); err != nil {
		return nil, err
	}
	var existingFingerprint string
	var ids []int64
	err = tx.QueryRowContext(ctx, `SELECT fingerprint,record_ids FROM intelligent_test_requests WHERE actor_id=$1 AND request_key=$2`, actor, req.IdempotencyKey).Scan(&existingFingerprint, pq.Array(&ids))
	if err == nil {
		if existingFingerprint != fingerprint {
			return nil, service.ErrIntelligentTestConflict
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+intelligentRecordColumns+` FROM account_tests t WHERE id=ANY($1) ORDER BY id`, pq.Array(ids))
		if err != nil {
			return nil, err
		}
		records, err := readIntelligentRecords(rows)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			compactIntelligentRecord(record)
		}
		return &service.IntelligentTestEnqueued{Records: records, Reused: true, ReusedCount: len(records)}, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var queued int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_tests WHERE status IN ('queued','running')`).Scan(&queued)
	if err != nil {
		return nil, err
	}
	settings := map[string]service.IntelligentTestConfig{}
	for _, kind := range types {
		var raw []byte
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled,config FROM test_settings WHERE test_type=$1 FOR SHARE`, kind).Scan(&enabled, &raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, infraerrors.BadRequest("INTELLIGENT_TEST_UNKNOWN_TYPE", "unknown test type")
			}
			return nil, err
		}
		if !enabled {
			return nil, infraerrors.BadRequest("INTELLIGENT_TEST_DISABLED", "selected test is disabled")
		}
		var cfg service.IntelligentTestConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		settings[kind] = cfg
	}
	out := &service.IntelligentTestEnqueued{Records: []*service.IntelligentTestRecord{}}
	for _, id := range accountIDs {
		var protected bool
		err := tx.QueryRowContext(ctx, `SELECT `+intelligentProtectionSQL+` FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL FOR SHARE`, id).Scan(&protected)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.BadRequest("INTELLIGENT_TEST_ACCOUNT_MISSING", "an account does not exist")
		}
		if err != nil {
			return nil, err
		}
		for _, kind := range types {
			cfg := settings[kind]
			cfg.Execution = nil
			if override := strings.TrimSpace(req.Models[kind]); override != "" {
				cfg.Model = override
			}
			active, activeErr := scanIntelligentRecord(tx.QueryRowContext(ctx, `SELECT `+intelligentRecordColumns+` FROM account_tests t WHERE account_id=$1 AND test_type=$2 AND status IN ('queued','running') FOR UPDATE`, id, kind))
			if activeErr == nil {
				if !service.SameIntelligentTestConfig(*active.ConfigSnapshot, cfg) {
					return nil, infraerrors.Conflict("INTELLIGENT_TEST_ACTIVE_CONFLICT", fmt.Sprintf("账号 %d 的 %s 已有不同模型或配置的任务 #%d；请等待完成或取消排队任务", id, kind, active.ID))
				}
				compactIntelligentRecord(active)
				out.Records = append(out.Records, active)
				ids = append(ids, active.ID)
				out.Reused = true
				out.ReusedCount++
				continue
			}
			if !errors.Is(activeErr, service.ErrIntelligentTestNotFound) {
				return nil, activeErr
			}
			if queued+out.CreatedCount >= 2000 {
				return nil, infraerrors.TooManyRequests("INTELLIGENT_TEST_QUEUE_FULL", "测试队列已满，请稍后重试")
			}
			cfgJSON, _ := json.Marshal(cfg)
			var testID int64
			err := tx.QueryRowContext(ctx, `INSERT INTO account_tests(account_id,test_type,input,model,anti_degradation,config_snapshot,requested_by) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, id, kind, cfg.Prompt, cfg.Model, protected, string(cfgJSON), actor).Scan(&testID)
			if err != nil {
				return nil, err
			}
			record, err := scanIntelligentRecord(tx.QueryRowContext(ctx, `SELECT `+intelligentRecordColumns+` FROM account_tests t WHERE id=$1`, testID))
			if err != nil {
				return nil, err
			}
			compactIntelligentRecord(record)
			out.Records = append(out.Records, record)
			ids = append(ids, testID)
			out.CreatedCount++
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intelligent_test_requests(actor_id,request_key,fingerprint,record_ids) VALUES($1,$2,$3,$4)`, actor, req.IdempotencyKey, fingerprint, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	if err := insertIntelligentAudit(ctx, tx, actor, "admin.intelligent_tests.enqueue", map[string]any{"account_ids": accountIDs, "test_types": types, "record_ids": ids, "count": len(ids), "idempotency_key": req.IdempotencyKey}); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (r *intelligentTestRepository) Claim(ctx context.Context) (*service.IntelligentTestRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(247001)`); err != nil {
		return nil, err
	}
	// An interrupted upstream call might already be billed. Never replay it automatically.
	_, err = tx.ExecContext(ctx, `UPDATE account_tests SET status='failed',error_message='服务中断或执行租约过期；请管理员手动重试，避免重复计费',finished_at=NOW(),duration_ms=GREATEST(0,(EXTRACT(EPOCH FROM (NOW()-started_at))*1000)::bigint),lease_token=NULL,lease_until=NULL WHERE status='running' AND lease_until<NOW()`)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_tests t SET status='cancelled',error_message='执行前测试已停用或账号已删除',finished_at=NOW() WHERE t.status='queued' AND (NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=t.account_id AND a.deleted_at IS NULL) OR NOT EXISTS(SELECT 1 FROM test_settings s WHERE s.test_type=t.test_type AND s.enabled))`)
	if err != nil {
		return nil, err
	}
	token := uuid.NewString()
	record, err := scanIntelligentRecord(tx.QueryRowContext(ctx, `WITH picked AS(SELECT q.id FROM account_tests q WHERE q.status='queued' AND q.available_at<=NOW() AND NOT EXISTS(SELECT 1 FROM account_tests running WHERE running.account_id=q.account_id AND running.status='running') AND (SELECT COUNT(*) FROM account_tests WHERE status='running')<4 ORDER BY q.available_at,q.id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE account_tests t SET status='running',queue_reason='',started_at=NOW(),lease_token=$1,lease_until=NOW()+make_interval(secs=>LEAST(600,GREATEST(30,(t.config_snapshot->>'timeout_seconds')::integer))+90),anti_degradation=`+`(SELECT `+intelligentProtectionSQL+` FROM accounts a WHERE a.id=t.account_id)`+` FROM picked WHERE t.id=picked.id RETURNING `+intelligentRecordColumns, token))
	if errors.Is(err, service.ErrIntelligentTestNotFound) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	return record, tx.Commit()
}
func (r *intelligentTestRepository) Finish(ctx context.Context, record *service.IntelligentTestRecord) error {
	if record.Status == "queued" || record.Status == "running" || record.Status == "" {
		return fmt.Errorf("cannot persist nonterminal test result")
	}
	eval, err := json.Marshal(record.Evaluation)
	if err != nil {
		return err
	}
	if string(eval) == "null" {
		eval = []byte("{}")
	}
	cfgJSON, err := json.Marshal(record.ConfigSnapshot)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE account_tests SET status=$3,score=$4,result=$5,result_image=$6,raw_response=$7,raw_truncated=$8,error_message=$9,duration_ms=$10,model=$11,config_snapshot=COALESCE($12::jsonb,config_snapshot),evaluation=$13,anti_degradation=$14,finished_at=NOW(),lease_token=NULL,lease_until=NULL WHERE id=$1 AND status='running' AND lease_token=$2`, record.ID, record.LeaseToken, record.Status, record.Score, record.Result, record.ResultImage, record.RawResponse, record.RawTruncated, record.ErrorMessage, record.DurationMS, record.Model, string(cfgJSON), string(eval), record.AntiDegradation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("test %d lost execution lease", record.ID)
	}
	now := time.Now().UTC()
	record.FinishedAt = &now
	return nil
}
