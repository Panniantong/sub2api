package service

import (
	"context"
	"maps"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrProtectionConflict = infraerrors.Conflict("PROTECTION_CONFLICT", "账号配置已变化，请刷新后重试")

type protectionWriteExpectationKey struct{}
type ProtectionWriteExpectation struct {
	AccountID int64
	UpdatedAt time.Time
}

func GetProtectionWriteExpectation(ctx context.Context) (ProtectionWriteExpectation, bool) {
	v, ok := ctx.Value(protectionWriteExpectationKey{}).(ProtectionWriteExpectation)
	return v, ok
}

// Calculate transitions without publishing an intermediate disabled account.
// The repository compares the original revision under its row lock before the
// single final update, outbox insert and transaction commit.
func (s *AntiDegradeService) transition(ctx context.Context, id int64, plan func(*AntiDegradeService) error) (*Account, error) {
	if s == nil || s.admin == nil {
		return nil, infraerrors.ServiceUnavailable("PROTECTION_UNAVAILABLE", "账号保护服务不可用")
	}
	original, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	draft := *original
	draft.Extra = maps.Clone(original.Extra)
	store := &protectionDraftStore{account: &draft}
	planner := &AntiDegradeService{admin: store, cfg: s.cfg, pluginManager: s.pluginManager}
	if err := plan(planner); err != nil {
		return nil, err
	}
	if !store.changed {
		return original, nil
	}
	ctx = context.WithValue(ctx, mode1ManagedWriteKey{}, true)
	ctx = context.WithValue(ctx, protectionWriteExpectationKey{}, ProtectionWriteExpectation{id, original.UpdatedAt})
	return s.admin.UpdateAccount(ctx, id, &UpdateAccountInput{Extra: store.account.Extra, Concurrency: &store.account.Concurrency})
}

type protectionDraftStore struct {
	account *Account
	changed bool
}

func (s *protectionDraftStore) GetAccount(context.Context, int64) (*Account, error) {
	a := *s.account
	a.Extra = maps.Clone(a.Extra)
	return &a, nil
}

func (s *protectionDraftStore) UpdateAccount(_ context.Context, _ int64, in *UpdateAccountInput) (*Account, error) {
	a := *s.account
	if in.Extra != nil {
		a.Extra = prepareCodexFingerprintExtraForUpdate(&a, in.Extra)
	}
	if in.Concurrency != nil {
		a.Concurrency = *in.Concurrency
	}
	BoundAccountProtectionConcurrency(&a)
	if err := ValidateAccountProtectionConfiguration(&a); err != nil {
		return nil, err
	}
	s.account, s.changed = &a, true
	return s.GetAccount(context.Background(), a.ID)
}
