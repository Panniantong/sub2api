package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const cookieRotationPoolKey = "openai_cookie_rotation_pool_runtime"

func (r *settingRepository) CookieRotationPool(ctx context.Context) (*service.CookieRotationPoolSnapshot, error) {
	raw, err := r.GetValue(ctx, cookieRotationPoolKey)
	if errors.Is(err, service.ErrSettingNotFound) {
		return &service.CookieRotationPoolSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	var result service.CookieRotationPoolSnapshot
	err = json.Unmarshal([]byte(raw), &result)
	return &result, err
}

// PostgreSQL row lock serializes pool membership across gateway replicas.
func (r *settingRepository) UpdateCookieRotationPool(ctx context.Context, update func(*service.CookieRotationPoolSnapshot) error) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Client().ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,'{}',now()) ON CONFLICT(key) DO NOTHING`, cookieRotationPoolKey)
	if err != nil {
		return err
	}
	rows, err := tx.Client().QueryContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, cookieRotationPoolKey)
	if err != nil {
		return err
	}
	var raw string
	if rows.Next() {
		err = rows.Scan(&raw)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	var snapshot service.CookieRotationPoolSnapshot
	if err = json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return err
	}
	if err = update(&snapshot); err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = tx.Client().ExecContext(ctx, `UPDATE settings SET value=$2,updated_at=now() WHERE key=$1`, cookieRotationPoolKey, string(data))
	if err != nil {
		return err
	}
	return tx.Commit()
}
