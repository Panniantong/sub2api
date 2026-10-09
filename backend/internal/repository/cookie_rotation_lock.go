package repository

import (
	"context"
	"fmt"
)

func (r *settingRepository) TryCookieRotationLock(ctx context.Context, identity string) (func(), bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, false, err
	}
	rows, err := tx.Client().QueryContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "cookie-host-rotation:"+identity)
	if err != nil {
		tx.Rollback()
		return nil, false, err
	}
	var acquired bool
	if rows.Next() {
		err = rows.Scan(&acquired)
	} else {
		err = fmt.Errorf("cookie rotation lock returned no row")
	}
	readErr := rows.Err()
	rows.Close()
	if err == nil {
		err = readErr
	}
	if err != nil || !acquired {
		tx.Rollback()
		return nil, false, err
	}
	return func() { _ = tx.Rollback() }, true, nil
}
