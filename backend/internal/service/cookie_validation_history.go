package service

import "context"

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
