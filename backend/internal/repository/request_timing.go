package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

type timingWrite struct {
	requestID string
	apiKeyID  int64
	data      requesttiming.Snapshot
}

// RecordRequestTiming is called after usage persistence has settled. Each real
// HTTP request has its own server-generated trace ID, including idempotent
// client retries sharing a single billed usage row.
func (r *usageLogRepository) RecordRequestTiming(ctx context.Context, requestID string, apiKeyID int64) {
	c := requesttiming.From(ctx)
	if c == nil || r.db == nil || requestID == "" {
		return
	}
	r.timingOnce.Do(func() { r.timingQueue = make(chan timingWrite, 512); go r.runTimingWriter() })
	c.WhenFinished(func(data requesttiming.Snapshot) {
		select {
		case r.timingQueue <- timingWrite{requestID, apiKeyID, data}:
		default:
			logger.LegacyPrintf("request_timing", "diagnostic queue full; detail dropped")
		}
	})
}
func (r *usageLogRepository) runTimingWriter() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case job := <-r.timingQueue:
			// All errors remain diagnostic-only. A short bounded retry handles transient
			// SQL failures; it never retries billing or a model request.
			for attempt := 0; attempt < 3; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err := r.writeTiming(ctx, job)
				cancel()
				if err == nil {
					break
				}
				if attempt == 2 {
					logger.LegacyPrintf("request_timing", "detail persistence failed: %v", err)
				}
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := r.db.ExecContext(ctx, `DELETE FROM request_timing_details WHERE (usage_log_id,trace_id) IN (
   SELECT d.usage_log_id,d.trace_id FROM request_timing_details d
   WHERE d.created_at < NOW() - INTERVAL '30 days' OR NOT EXISTS (SELECT 1 FROM usage_logs u WHERE u.id=d.usage_log_id)
   LIMIT 10000)`)
			cancel()
			if err != nil {
				logger.LegacyPrintf("request_timing", "detail retention failed: %v", err)
			}
		}
	}
}
func (r *usageLogRepository) writeTiming(ctx context.Context, job timingWrite) error {
	raw, err := json.Marshal(job.data)
	if err != nil {
		return err
	}
	if len(raw) > 128*1024 {
		return fmt.Errorf("timing payload exceeds bound")
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,detail)
 SELECT id,$1,$2::jsonb FROM usage_logs WHERE request_id=$3 AND api_key_id=$4
 ON CONFLICT (usage_log_id,trace_id) DO UPDATE SET detail=EXCLUDED.detail`, job.data.TraceID, string(raw), job.requestID, job.apiKeyID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("usage row unavailable")
	}
	return err
}
func (r *usageLogRepository) RequestTimings(ctx context.Context, id int64) ([]json.RawMessage, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT detail FROM request_timing_details WHERE usage_log_id=$1 ORDER BY created_at DESC,trace_id LIMIT 20`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}
