package service

import (
	"context"
	"net/http"
	"time"
)

// Refresh after admission/queueing, before constructing outbound headers. Use
// the database as the authority: a scheduler snapshot may predate a Host change.
// Return a request-local account; never mutate a shared scheduler snapshot.
func (s *OpenAIGatewayService) refreshCookieDispatchAccount(ctx context.Context, account *Account) (*Account, error) {
	if s == nil || s.settingService == nil || !isOpenAICodexTicketAccount(account) || account.openaiCookieValidationCookie != "" {
		return account, nil
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return nil, cookieDispatchUnavailable()
	}
	guarded, _ := openAICookieSchedulingStatus(account, settings, time.Now())
	if !guarded && account.GetExtraString(openAICodexCookieHostExtraKey) == "" && !account.openaiCookieDegraded && !settings.CookieRotationEnabled {
		return account, nil
	}
	if s.accountRepo == nil {
		return nil, cookieDispatchUnavailable()
	}
	latest, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || latest == nil || !latest.IsSchedulable() {
		return nil, cookieDispatchUnavailable()
	}
	if s.openAICookieSchedulingBlockReason(ctx, latest) != "" {
		return nil, cookieDispatchUnavailable()
	}
	if group, ok := ctx.Value(cookieSchedulingGroupKey{}).(int64); ok && s.cookieDegradationConfigured(ctx) {
		if !s.openAIAccountMatchesSchedulingGroup(ctx, latest, &group) {
			return nil, cookieDispatchUnavailable()
		}
	}
	copy := *latest
	copy.Extra = make(map[string]any, len(latest.Extra))
	for k, v := range latest.Extra {
		copy.Extra[k] = v
	}
	current := s.cookieDegradedRequestAccount(ctx, &copy)
	if !current.openaiCookieDegraded && account.GetExtraString(openAICodexCookieHostExtraKey) != "" && openAICodexCookieHostFromAccount(current) == "" {
		return nil, cookieDispatchUnavailable()
	}
	if host := openAICodexCookieHostFromAccount(current); host != "" {
		entry, err := s.settingService.LookupOpenAICodexCookie(ctx, host)
		if err != nil || entry == nil {
			return nil, cookieDispatchUnavailable()
		}
	}
	return current, nil
}

func cookieDispatchUnavailable() *UpstreamFailoverError {
	return &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RequestScopedTransient: true,
		ResponseBody: []byte(`{"error":{"type":"cookie_binding_changed","message":"Cookie Host binding unavailable; reselect account"}}`)}
}

// A persistent upstream WS connection carries its original Cookie handshake.
// Never send another turn over it after a binding or routing-group transition.
func (s *OpenAIGatewayService) checkCookieDispatchTurn(ctx context.Context, account *Account) error {
	latest, err := s.refreshCookieDispatchAccount(ctx, account)
	if err != nil {
		return err
	}
	if latest != nil && account != nil && (openAICodexCookieHostFromAccount(latest) != openAICodexCookieHostFromAccount(account) || latest.openaiCookieDegraded != account.openaiCookieDegraded) {
		return cookieDispatchUnavailable()
	}
	return nil
}
