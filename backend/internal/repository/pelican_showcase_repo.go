package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type pelicanShowcaseRepository struct {
	db *sql.DB
}

func NewPelicanShowcaseRepository(db *sql.DB) service.PelicanShowcaseRepository {
	return &pelicanShowcaseRepository{db: db}
}

// Publish copies the result into every listed group the plan's (not deleted) account
// belongs to, then trims only those groups. Both steps share a transaction so a group
// never keeps more than maxItems snapshots.
func (r *pelicanShowcaseRepository) Publish(ctx context.Context, result *service.ScheduledTestResult, groupIDs []int64, maxItems int) error {
	var modelID, effort string
	if cfg := result.PelicanConfig; cfg != nil {
		modelID, effort = cfg.ModelID, cfg.ReasoningEffort
	}
	generatedAt := result.StartedAt
	if generatedAt.IsZero() {
		generatedAt = result.CreatedAt
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `INSERT INTO pelican_showcase_items
 (group_id, source_result_id, model_id, reasoning_effort, response_text, latency_ms, generated_at)
 SELECT DISTINCT ag.group_id, $2::bigint, $3::text, $4::text, $5::text, $6::bigint, $7::timestamptz
 FROM scheduled_test_plans p
 JOIN accounts a ON a.id = p.account_id AND a.deleted_at IS NULL
 JOIN account_groups ag ON ag.account_id = p.account_id
 WHERE p.id = $1 AND ag.group_id = ANY($8)
 ON CONFLICT (group_id, source_result_id) DO NOTHING
 RETURNING group_id`, result.PlanID, result.ID, modelID, effort, result.ResponseText, result.LatencyMs, generatedAt, pq.Array(groupIDs))
	if err != nil {
		return err
	}
	published := make([]int64, 0, len(groupIDs))
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			_ = rows.Close()
			return err
		}
		published = append(published, groupID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(published) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pelican_showcase_items WHERE id IN (
 SELECT id FROM (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY generated_at DESC, id DESC) AS rn
  FROM pelican_showcase_items WHERE group_id = ANY($1)
 ) ranked WHERE rn > $2)`, pq.Array(published), maxItems); err != nil {
		return err
	}
	return tx.Commit()
}

// Prune runs every minute; bounded batches keep each transaction short.
func (r *pelicanShowcaseRepository) Prune(ctx context.Context, groupIDs []int64, maxItems int, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM pelican_showcase_items WHERE id IN (
 SELECT id FROM (
  SELECT id, group_id, generated_at,
   ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY generated_at DESC, id DESC) AS rn
  FROM pelican_showcase_items
 ) ranked
 WHERE NOT (group_id = ANY($1)) OR rn > $2 OR ($3::timestamptz IS NOT NULL AND generated_at < $3)
 LIMIT 1000)`, pq.Array(groupIDs), maxItems, nullableTime(before))
	return err
}

func (r *pelicanShowcaseRepository) ListGroups(ctx context.Context, groupIDs []int64) ([]*service.PelicanShowcaseGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, platform FROM groups
 WHERE id = ANY($1) AND deleted_at IS NULL AND status = $2
 ORDER BY sort_order ASC, id ASC`, pq.Array(groupIDs), service.StatusActive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	groups := make([]*service.PelicanShowcaseGroup, 0, len(groupIDs))
	for rows.Next() {
		group := &service.PelicanShowcaseGroup{}
		if err := rows.Scan(&group.ID, &group.Name, &group.Platform); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (r *pelicanShowcaseRepository) ListItems(ctx context.Context, groupIDs []int64, maxItems int, since time.Time) ([]*service.PelicanShowcaseItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, group_id, model_id, reasoning_effort, latency_ms, generated_at FROM (
 SELECT id, group_id, model_id, reasoning_effort, latency_ms, generated_at,
  ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY generated_at DESC, id DESC) AS rn
 FROM pelican_showcase_items WHERE group_id = ANY($1)
) ranked
WHERE rn <= $2 AND ($3::timestamptz IS NULL OR generated_at >= $3)
ORDER BY group_id, generated_at DESC, id DESC`, pq.Array(groupIDs), maxItems, nullableTime(since))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]*service.PelicanShowcaseItem, 0)
	for rows.Next() {
		item := &service.PelicanShowcaseItem{}
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ModelID, &item.ReasoningEffort, &item.LatencyMs, &item.GeneratedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetItem applies the same visibility as ListItems: a listed, active group, inside the
// retention window and among the group's newest maxItems.
func (r *pelicanShowcaseRepository) GetItem(ctx context.Context, id int64, groupIDs []int64, maxItems int, since time.Time) (*service.PelicanShowcaseItem, error) {
	item := &service.PelicanShowcaseItem{}
	err := r.db.QueryRowContext(ctx, `SELECT i.id, i.group_id, i.model_id, i.reasoning_effort, i.latency_ms, i.generated_at, i.response_text
 FROM pelican_showcase_items i
 JOIN groups g ON g.id = i.group_id AND g.deleted_at IS NULL AND g.status = $5
 WHERE i.id = $1 AND i.group_id = ANY($2)
 AND ($4::timestamptz IS NULL OR i.generated_at >= $4)
 AND (SELECT COUNT(*) FROM pelican_showcase_items newer WHERE newer.group_id = i.group_id
  AND (newer.generated_at, newer.id) > (i.generated_at, i.id)) < $3`,
		id, pq.Array(groupIDs), maxItems, nullableTime(since), service.StatusActive).Scan(
		&item.ID, &item.GroupID, &item.ModelID, &item.ReasoningEffort, &item.LatencyMs, &item.GeneratedAt, &item.ResponseText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *pelicanShowcaseRepository) Delete(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM pelican_showcase_items WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
