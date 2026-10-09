package service

import (
	"context"
	"fmt"
	"time"
)

// Implemented with a transaction-scoped PostgreSQL advisory lock. Closing the
// transaction (including a process disconnect) releases the mailbox automatically.
type CookieRotationLockRepository interface {
	TryCookieRotationLock(context.Context, string) (func(), bool, error)
}

func (s *OpenAIGatewayService) lockCookieRotation(ctx context.Context, a *Account) (func(), bool, error) {
	if s.settingService != nil {
		if repo, ok := s.settingService.settingRepo.(CookieRotationLockRepository); ok {
			return repo.TryCookieRotationLock(ctx, cookiePoolIdentity(a))
		}
	}
	return func() {}, true, nil
}

func (s *OpenAIGatewayService) cookieValidationAdmission(ctx context.Context, a *Account, original *OpenAICookieSettings, expected CookieRotationPoolMember) (*OpenAICookieSettings, error) {
	if cookiePoolPolicy(a, original) == nil {
		return original, nil
	}
	latest, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return nil, err
	}
	// A cached settings value may be up to one second old; final dispatch also
	// checks current pool admission independently of the candidate binding.
	if !latest.Enabled || !latest.CookieRotationEnabled || cookieHostMonitorOwns(a, latest) {
		return nil, fmt.Errorf("cookie rotation disabled")
	}
	if s.accountRepo != nil {
		fresh, err := s.accountRepo.GetByID(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if fresh == nil || !fresh.IsSchedulable() || cookiePoolIdentity(fresh) != cookiePoolIdentity(a) {
			return nil, fmt.Errorf("cookie account changed")
		}
		before, after := cookiePoolPolicy(a, original), cookiePoolPolicy(fresh, latest)
		if after == nil || after.GroupID != before.GroupID {
			return nil, fmt.Errorf("cookie pool ownership changed")
		}
		if openAICodexCookieHostFromAccount(fresh) != openAICodexCookieHostFromAccount(a) {
			return nil, fmt.Errorf("cookie binding changed during validation")
		}
	}
	member, _ := cookiePoolMember(a, latest)
	if (member.State != "active" && member.State != "preparing") || !member.Since.Equal(expected.Since) {
		return nil, fmt.Errorf("cookie pool admission revoked")
	}
	if member.State == "preparing" && time.Since(member.Since) >= time.Duration(cookiePoolPolicy(a, latest).ReadyTimeoutSeconds)*time.Second {
		return nil, fmt.Errorf("cookie pool preparation timed out")
	}
	return latest, nil
}
