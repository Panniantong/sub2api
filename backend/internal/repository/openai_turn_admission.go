package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.OpenAITurnAdmissionReader = (*accountRepository)(nil)

// GetByID hydrates groups/proxy in separate queries. A read-only repeatable-read
// transaction makes those queries (and the shadow parent) one coherent snapshot.
func (r *accountRepository) GetOpenAITurnAdmission(ctx context.Context, id int64) (*service.Account, *service.Account, error) {
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	reader := &accountRepository{client: tx.Client()}
	account, err := reader.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	var parent *service.Account
	if account.IsShadow() {
		parent, err = reader.GetByID(ctx, *account.ParentAccountID)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return account, parent, nil
}
