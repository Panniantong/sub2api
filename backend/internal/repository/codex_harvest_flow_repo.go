package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const harvestFlowPersistCap = 200

type codexHarvestFlowRepository struct{ db *sql.DB }

func NewCodexHarvestFlowRepository(db *sql.DB) service.CodexHarvestFlowRepository {
	return &codexHarvestFlowRepository{db: db}
}

func (r *codexHarvestFlowRepository) List(ctx context.Context, limit int) ([]service.CodexHarvestFlowEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("harvest flow repository unavailable")
	}
	if limit < 1 || limit > harvestFlowPersistCap {
		limit = harvestFlowPersistCap
	}
	rows, err := r.db.QueryContext(ctx, `SELECT event_id, at, stage, kind, account_id, account_name, model, node,
 http_status, length, blocks, expected_length, expected_blocks, accepted, standby, result, reason, detail
 FROM codex_harvest_flow_events ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	newestFirst := make([]service.CodexHarvestFlowEvent, 0, limit)
	for rows.Next() {
		var event service.CodexHarvestFlowEvent
		if err := rows.Scan(&event.ID, &event.At, &event.Stage, &event.Kind, &event.AccountID, &event.AccountName, &event.Model, &event.Node,
			&event.HTTPStatus, &event.Length, &event.Blocks, &event.ExpectedLength, &event.ExpectedBlocks, &event.Accepted, &event.Standby, &event.Result, &event.Reason, &event.Detail); err != nil {
			return nil, err
		}
		newestFirst = append(newestFirst, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]service.CodexHarvestFlowEvent, len(newestFirst))
	for i := range newestFirst {
		out[len(newestFirst)-1-i] = newestFirst[i]
	}
	return out, nil
}

func (r *codexHarvestFlowRepository) Append(ctx context.Context, event service.CodexHarvestFlowEvent) error {
	if r == nil || r.db == nil {
		return errors.New("harvest flow repository unavailable")
	}
	if event.ID == "" {
		return errors.New("harvest flow event id required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO codex_harvest_flow_events
 (event_id, at, stage, kind, account_id, account_name, model, node, http_status, length, blocks,
 expected_length, expected_blocks, accepted, standby, result, reason, detail)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
 ON CONFLICT (event_id) DO NOTHING`,
		event.ID, event.At, event.Stage, event.Kind, event.AccountID, event.AccountName, event.Model, event.Node,
		event.HTTPStatus, event.Length, event.Blocks, event.ExpectedLength, event.ExpectedBlocks,
		event.Accepted, event.Standby, event.Result, event.Reason, event.Detail); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM codex_harvest_flow_events WHERE id IN (
 SELECT id FROM codex_harvest_flow_events ORDER BY at DESC, id DESC OFFSET $1)`, harvestFlowPersistCap); err != nil {
		return err
	}
	return tx.Commit()
}
