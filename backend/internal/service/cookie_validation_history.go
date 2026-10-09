package service

import (
	"context"
	"fmt"
)

type CookieValidationHistoryRepository interface {
	AppendCookieValidation(context.Context, OpenAICookieAcquisitionLog) error
	CookieValidationPage(context.Context, int64, string, int, int) ([]OpenAICookieAcquisitionLog, int64, error)
	CookieValidationHosts(context.Context, int64) ([]string, error)
}

func (s *SettingService) CookieValidationHosts(ctx context.Context, accountID int64) ([]string, error) {
	if repo, ok := s.settingRepo.(CookieValidationHistoryRepository); ok {
		return repo.CookieValidationHosts(ctx, accountID)
	}
	return []string{}, nil
}

func (s *SettingService) CookieValidationEmailPage(ctx context.Context, email, host string, page, size int) ([]OpenAICookieAcquisitionLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	if repo, ok := s.settingRepo.(interface {
		CookieValidationEmailPage(context.Context, int64, string, string, int, int) ([]OpenAICookieAcquisitionLog, int64, error)
	}); ok {
		return repo.CookieValidationEmailPage(ctx, 0, email, host, page, size)
	}
	return nil, 0, fmt.Errorf("email history storage unavailable")
}
