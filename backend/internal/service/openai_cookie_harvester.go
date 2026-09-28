package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	openAICodexCookieRotationStatusExtraKey  = "codex_cookie_rotation_status"
	openAICodexCookieRotationStartedExtraKey = "codex_cookie_rotation_started_at"
	openAICodexCookieRotationMessageExtraKey = "codex_cookie_rotation_message"
)

// autoConfigureOpenAICookieHost validates a freshly harvested Host only for
// accounts that do not already have a Host. Cooldown and already-bound skips
// are intentionally silent because this function runs on every harvest cycle.
func (s *OpenAIGatewayService) autoConfigureOpenAICookieHost(ctx context.Context, source *Account, settings *OpenAICookieSettings, host, cookie, proxyURL, model string) {
	if s == nil || source == nil || settings == nil || !settings.Enabled || !settings.CookieRotationEnabled || s.accountRepo == nil {
		return
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		slog.Warn("cookie auto host account scan failed", "error", err)
		return
	}
	normalizedHost := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for i := range accounts {
		account := &accounts[i]
		if account.ID == source.ID || account.Status != StatusActive || !isOpenAICodexTicketAccount(account) || isOpenAICookieCollector(account, settings) || !isOpenAICookieRotationAccount(account, settings) {
			continue
		}
		boundHost := openAICodexCookieHostFromAccount(account)
		if boundHost != "" {
			if !settings.CookieRotationEnabled || !openAICookieRotationDue(account, time.Now()) || boundHost == normalizedHost {
				continue
			}
		}
		if until := openAICodexCookieHostCooldownUntil(account, normalizedHost); until.After(time.Now()) {
			continue
		}
		_, _ = s.validateBindAndBuildOpenAICookieHost(ctx, account, settings, normalizedHost, cookie, proxyURL, model, "")
	}
}

func isOpenAICookieRotationAccount(account *Account, settings *OpenAICookieSettings) bool {
	if cookieHostMonitorOwns(account, settings) {
		return false
	}
	if account == nil || settings == nil {
		return false
	}
	// Rotation is an explicit opt-in scope. An empty account/group selection
	// must not silently promote every account into the rotation pool.
	if len(settings.RotationAccountIDs) == 0 && len(settings.RotationGroupIDs) == 0 {
		return false
	}
	for _, id := range settings.RotationAccountIDs {
		if id == account.ID {
			return true
		}
	}
	return isOpenAICookieRotationGroupAccount(account, settings)
}

// Scheduling protection deliberately uses only the group selection, not the
// individual accounts that may independently opt into rotation.
func isOpenAICookieRotationGroupAccount(account *Account, settings *OpenAICookieSettings) bool {
	if account == nil || settings == nil || len(settings.RotationGroupIDs) == 0 {
		return false
	}
	groupIDs := append([]int64(nil), account.GroupIDs...)
	for _, accountGroup := range account.AccountGroups {
		groupIDs = append(groupIDs, accountGroup.GroupID)
	}
	for _, group := range account.Groups {
		if group != nil {
			groupIDs = append(groupIDs, group.ID)
		}
	}
	for _, accountGroupID := range groupIDs {
		for _, selectedGroupID := range settings.RotationGroupIDs {
			if selectedGroupID > 0 && accountGroupID == selectedGroupID {
				return true
			}
		}
	}
	return false
}

func (s *OpenAIGatewayService) appendOpenAICookieValidationLog(ctx context.Context, item OpenAICookieAcquisitionLog) {
	if s == nil || s.settingService == nil {
		return
	}
	logCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.settingService.appendOpenAICookieLog(logCtx, item); err != nil {
		slog.Warn("cookie validation log persist failed", "account_id", item.AccountID, "error", err)
	}
}

func newOpenAICookieValidationAttempt(account *Account, host, proxyURL, model string) OpenAICookieAcquisitionLog {
	attemptID := uuid.NewString()
	item := OpenAICookieAcquisitionLog{
		AttemptID: attemptID, AccountID: account.ID, AccountName: account.Name,
		Model: model, Proxy: sanitizeOpenAICookieProxyForDisplay(proxyURL), Host: normalizeOpenAICookieHost(host),
		ValidationEnabled: true, Kind: "validation", BindingHost: normalizeOpenAICookieHost(host),
	}
	if parsed, err := url.Parse(proxyURL); err == nil && parsed.User != nil {
		item.ProxyUsername = parsed.User.Username()
	}
	return item
}

func (s *OpenAIGatewayService) appendOpenAICookieValidationStage(ctx context.Context, base OpenAICookieAcquisitionLog, stage, message string) {
	base.ID = uuid.NewString()
	base.CreatedAt = time.Now()
	base.Stage = stage
	base.Message = message
	s.appendOpenAICookieValidationLog(ctx, base)
}

func openAICookieRotationDue(account *Account, now time.Time) bool {
	if account == nil || account.Extra == nil {
		return true
	}
	value := strings.TrimSpace(account.GetExtraString(openAICodexCookieRotationNextExtraKey))
	if value == "" {
		return true
	}
	next, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || !next.After(now)
}

func (s *OpenAIGatewayService) scheduleOpenAICookieRotation(ctx context.Context, account *Account, bindingSeconds, rotationBeforeSeconds int) {
	if s == nil || account == nil || bindingSeconds < 1 {
		return
	}
	if rotationBeforeSeconds < 0 || rotationBeforeSeconds > bindingSeconds {
		rotationBeforeSeconds = 0
	}
	next := time.Now().Add(time.Duration(bindingSeconds-rotationBeforeSeconds) * time.Second)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	value := next.Format(time.RFC3339Nano)
	account.Extra[openAICodexCookieRotationNextExtraKey] = value
	if s.accountRepo != nil {
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{openAICodexCookieRotationNextExtraKey: value}); err != nil {
			slog.Warn("cookie rotation schedule persist failed", "account_id", account.ID, "error", err)
		}
	}
}

// rotateDueOpenAICookieHost actively tries another valid Cookie library entry
// when the account binding reaches its rotation window. Relying only on the
// next proxy harvest can leave an account stuck when that proxy keeps
// returning the currently bound Host.
func (s *OpenAIGatewayService) rotateDueOpenAICookieHost(ctx context.Context, account *Account, settings *OpenAICookieSettings) bool {
	if s == nil || account == nil || settings == nil || !settings.CookieRotationEnabled {
		return false
	}
	current := openAICodexCookieHostFromAccount(account)
	if !openAICookieRotationDue(account, time.Now()) {
		return false
	}
	lockValue, _ := s.openaiCookieRotationLocks.LoadOrStore(account.ID, &sync.Mutex{})
	rotationLock := lockValue.(*sync.Mutex)
	if !rotationLock.TryLock() {
		return true
	}
	defer rotationLock.Unlock()
	s.openaiCookieRotationRunning.Store(account.ID, true)
	defer s.openaiCookieRotationRunning.Delete(account.ID)
	// Re-read the due state after taking the account lock. The harvester and
	// an explicit refresh can reach this function concurrently.
	current = openAICodexCookieHostFromAccount(account)
	if !openAICookieRotationDue(account, time.Now()) {
		return false
	}
	s.setOpenAICookieRotationState(ctx, account, "running", "开始尝试下一个 Cookie Host")
	startMessage := "账号未绑定 Host，开始寻找可用 Cookie Host"
	if current != "" {
		startMessage = fmt.Sprintf("当前 Host %s 已到轮换时间，开始尝试下一个 Host", current)
	}
	previousRotationState := strings.TrimSpace(account.GetExtraString(openAICodexCookieRotationStatusExtraKey))
	validationModel := s.settingService.GetOpenAIIntelligenceModel(ctx)
	baseLog := OpenAICookieAcquisitionLog{ID: uuid.NewString(), AttemptID: uuid.NewString(), AccountID: account.ID, AccountName: account.Name, Model: validationModel, CreatedAt: time.Now(), Host: current, Kind: "validation", BindingHost: current, BindingStatus: "rotation_started", ValidationEnabled: true, Message: startMessage}
	if previousRotationState != "waiting" {
		s.appendOpenAICookieValidationLog(ctx, baseLog)
	}
	entries, err := s.settingService.GetOpenAICodexCookieLibrary(ctx)
	if err != nil {
		s.setOpenAICookieRotationState(ctx, account, "waiting", "读取 Cookie 库失败，稍后重试")
		return false
	}
	now := time.Now()
	attempted := 0
	for _, entry := range entries {
		host := normalizeOpenAICookieHost(entry.Host)
		if !openAICookieRotationCandidate(account, entry, now) {
			continue
		}
		attempted++
		s.appendOpenAICookieValidationLog(ctx, OpenAICookieAcquisitionLog{ID: uuid.NewString(), AttemptID: baseLog.AttemptID, AccountID: account.ID, AccountName: account.Name, Model: validationModel, CreatedAt: time.Now(), Host: host, Kind: "validation", BindingHost: host, BindingStatus: "rotation_candidate", ValidationEnabled: true, Message: fmt.Sprintf("轮换尝试 Host %s（模型 %s）", host, validationModel)})
		result, _ := s.validateBindAndBuildOpenAICookieHost(ctx, account, settings, host, entry.Cookie, "", validationModel, "")
		if result == "yes" {
			s.setOpenAICookieRotationState(ctx, account, "success", fmt.Sprintf("已切换到 Host %s", host))
			return true
		}
	}
	message := "当前没有可用的其他 Host，稍后重试"
	if attempted > 0 {
		message = fmt.Sprintf("已尝试 %d 个候选 Host，均未通过验证，稍后重试", attempted)
	}
	s.setOpenAICookieRotationState(ctx, account, "waiting", message)
	// Keep the scheduler checking frequently so a newly acquired Host is picked
	// up quickly, but suppress duplicate start/wait records while the account
	// remains in the same empty/cooldown state.
	s.scheduleOpenAICookieRotation(ctx, account, 15, 0)
	if previousRotationState != "waiting" || attempted > 0 {
		s.appendOpenAICookieValidationLog(ctx, OpenAICookieAcquisitionLog{ID: uuid.NewString(), AccountID: account.ID, AccountName: account.Name, Model: validationModel, CreatedAt: time.Now(), Host: current, Kind: "validation", BindingHost: current, BindingStatus: "rotation_waiting", ValidationEnabled: true, Message: message})
	}
	return false
}

func (s *OpenAIGatewayService) setOpenAICookieRotationState(ctx context.Context, account *Account, state, message string) {
	if account == nil {
		return
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[openAICodexCookieRotationStatusExtraKey] = state
	account.Extra[openAICodexCookieRotationMessageExtraKey] = message
	updates := map[string]any{openAICodexCookieRotationStatusExtraKey: state, openAICodexCookieRotationMessageExtraKey: message}
	if state == "running" {
		started := time.Now().Format(time.RFC3339Nano)
		account.Extra[openAICodexCookieRotationStartedExtraKey] = started
		updates[openAICodexCookieRotationStartedExtraKey] = started
	}
	if s.accountRepo != nil {
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
			slog.Warn("cookie rotation state persist failed", "account_id", account.ID, "error", err)
		}
	}
}

func (s *OpenAIGatewayService) validateBindAndBuildOpenAICookieHost(ctx context.Context, account *Account, settings *OpenAICookieSettings, host, cookie, proxyURL, model, token string) (string, error) {
	// Host capability validation always uses the independent intelligence
	// model. The Cookie acquisition model must never leak into this request.
	model = s.settingService.GetOpenAIIntelligenceModel(ctx)
	if strings.TrimSpace(proxyURL) == "" && account != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	base := newOpenAICookieValidationAttempt(account, host, proxyURL, model)
	previousHost := openAICodexCookieHostFromAccount(account)
	s.appendOpenAICookieValidationStage(ctx, base, "host_selected", fmt.Sprintf("账号准备绑定 Host %s", base.Host))

	if strings.TrimSpace(token) == "" {
		var err error
		token, _, err = s.GetAccessToken(ctx, account)
		if err != nil || strings.TrimSpace(token) == "" {
			if err == nil {
				err = fmt.Errorf("access token is empty")
			}
			base.BindingStatus = "token_failed"
			base.ValidationResult = "error"
			s.appendOpenAICookieValidationStage(ctx, base, "validation_failed", "无法发送验证提示词："+err.Error())
			return "error", err
		}
	}

	// Attach the candidate to a request-local account. The live account keeps
	// its old deadline/Host until validation succeeds, so early rotation can
	// continue serving the old binding and expired/unbound accounts stay gated.
	candidate := *account
	candidate.openaiCookieValidationCookie = cookie
	candidate.Extra = make(map[string]any, len(account.Extra)+1)
	for key, value := range account.Extra {
		candidate.Extra[key] = value
	}
	candidate.Extra[openAICodexCookieHostExtraKey] = base.Host
	base.BindingStatus = "bound_for_validation"
	s.appendOpenAICookieValidationStage(ctx, base, "host_cookie_bound", "候选 Host Cookie 已用于账号验证请求，通过后更新正式绑定")
	s.appendOpenAICookieValidationStage(ctx, base, "validation_started", "开始发送 yes/no 验证提示词")
	// The account/Host binding clock starts when the validation request is
	// actually initiated, rather than when the temporary candidate is attached.
	validationStartedAt := time.Now()
	// Network/upstream failures are retried twice. A definitive "no" is not
	// retried because it is a valid Host capability result and should enter
	// cooldown immediately.
	result, responseBody, statusCode := "error", "", 0
	for attempt := 0; attempt < 3; attempt++ {
		result, responseBody, statusCode = s.validateOpenAICodexCookieHost(ctx, &candidate, token, cookie, "", model)
		if result == "yes" || result == "no" || attempt == 2 {
			break
		}
		retryLog := base
		retryLog.ValidationResult = result
		retryLog.StatusCode = statusCode
		retryLog.ValidationResponse = truncateOpenAICodexValidationResponse(responseBody)
		s.appendOpenAICookieValidationStage(ctx, retryLog, "validation_retry", fmt.Sprintf("验证请求异常，第 %d 次重试", attempt+1))
	}
	base.StatusCode = statusCode
	base.ValidationResult = result
	base.ValidationResponse = truncateOpenAICodexValidationResponse(responseBody)
	switch result {
	case "yes":
		base.Success = true
		s.appendOpenAICookieValidationStage(ctx, base, "validation_succeeded", "验证响应为 yes，账号可使用该 Host")
	case "no":
		base.Success = false
		base.BindingStatus = "cooldown"
		cooldownErr := s.markOpenAICodexCookieHostCooldown(ctx, account, base.Host, settings.WSHostCooldownSeconds)
		message := "验证响应为 no，账号与该 Host 已进入冷静期，将继续尝试其他 Host"
		if cooldownErr != nil {
			message += "；冷静期保存失败: " + cooldownErr.Error()
		}
		s.appendOpenAICookieValidationStage(ctx, base, "validation_rejected", message)
		return result, nil
	default:
		base.Success = false
		base.BindingStatus = "validation_failed"
		s.appendOpenAICookieValidationStage(ctx, base, "validation_failed", "验证请求失败或响应不是 yes/no")
		return result, nil
	}

	if err := s.bindOpenAICodexCookieHostStartedAt(ctx, account, base.Host, validationStartedAt); err != nil {
		base.Success = false
		base.BindingStatus = "bind_failed"
		s.appendOpenAICookieValidationStage(ctx, base, "host_bind_failed", "验证通过，但绑定 Host 失败: "+err.Error())
		return "error", err
	}
	base.BindingStatus = "bound"
	s.appendOpenAICookieValidationStage(ctx, base, "host_bound", "Host 已绑定到账号")

	if previousHost != "" && previousHost != base.Host {
		if err := s.markOpenAICodexCookieHostCooldown(ctx, account, previousHost, settings.WSHostCooldownSeconds); err != nil {
			slog.Warn("previous cookie host cooldown persist failed", "account_id", account.ID, "host", previousHost, "error", err)
		}
		s.appendOpenAICookieValidationStage(ctx, base, "previous_host_cooldown", fmt.Sprintf("旧 Host %s 已进入冷静期", previousHost))
	}
	if !settings.WSEnabled || settings.CookieRotationEnabled {
		s.appendOpenAICookieValidationStage(ctx, base, "ws_build_skipped", "Cookie WS 未启用，跳过构建")
		return result, nil
	}
	s.appendOpenAICookieValidationStage(ctx, base, "ws_build_started", "开始构建 Cookie WS 连接")
	callbackBase := base
	err := s.scheduleCookieWSBinding(ctx, account, func(live, target int, buildErr error) {
		logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if buildErr != nil {
			callbackBase.Success = false
			callbackBase.BindingStatus = "bound_ws_failed"
			s.appendOpenAICookieValidationStage(logCtx, callbackBase, "ws_build_failed", "WS 构建失败: "+buildErr.Error())
			return
		}
		callbackBase.Success = true
		callbackBase.BindingStatus = "bound"
		s.appendOpenAICookieValidationStage(logCtx, callbackBase, "ws_build_succeeded", fmt.Sprintf("WS 构建完成，已建立 %d/%d 条连接", live, target))
	})
	if err != nil {
		base.Success = false
		base.BindingStatus = "bound_ws_failed"
		s.appendOpenAICookieValidationStage(ctx, base, "ws_build_failed", "WS 构建启动失败: "+err.Error())
		return "error", err
	}
	return result, nil
}

// ValidateAndBindOpenAICookieHost is the manual-selection counterpart of the
// harvester validation path. It is intentionally synchronous so the account
// update cannot report a Host as bound before the yes/no decision is known.
func (s *OpenAIGatewayService) ValidateAndBindOpenAICookieHost(ctx context.Context, account *Account, host string) (string, error) {
	if err := s.checkCookieHostMonitorLock(ctx, account); err != nil {
		return "error", err
	}
	if s == nil || account == nil {
		return "error", fmt.Errorf("account is unavailable")
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return "error", err
	}
	host = normalizeOpenAICookieHost(host)
	if host == "" {
		return "error", fmt.Errorf("host is empty")
	}
	if !settings.AutoValidateHost && !settings.CookieRotationEnabled {
		return "disabled", nil
	}
	if currentHost := openAICodexCookieHostFromAccount(account); currentHost != "" {
		if currentHost == host {
			return "already_bound", nil
		}
		return "error", fmt.Errorf("账号已绑定 Host %s，请先解绑后再选择其他 Host", currentHost)
	}
	if until := openAICodexCookieHostCooldownUntil(account, host); until.After(time.Now()) {
		return "skipped", fmt.Errorf("账号与 Host %s 正在冷静期，至 %s", host, until.Format(time.RFC3339))
	}
	entry, err := s.settingService.LookupOpenAICodexCookie(ctx, host)
	if err != nil {
		return "error", err
	}
	if entry == nil {
		err = fmt.Errorf("Cookie Host %s 不存在或已过期", host)
		return "error", err
	}
	return s.validateBindAndBuildOpenAICookieHost(ctx, account, settings, host, entry.Cookie, "", settings.Model, "")
}

func (s *OpenAIGatewayService) LogOpenAICookieHostBinding(ctx context.Context, account *Account, host, status, message string, success bool) {
	if account == nil {
		return
	}
	s.appendOpenAICookieValidationLog(ctx, OpenAICookieAcquisitionLog{
		ID: uuid.NewString(), AccountID: account.ID, AccountName: account.Name,
		CreatedAt: time.Now(), Host: normalizeOpenAICookieHost(host),
		Kind: "validation", BindingHost: normalizeOpenAICookieHost(host), BindingStatus: status,
		ValidationEnabled: false, ValidationResult: "disabled", Success: success, Message: message,
	})
}

// Independent schedule and settings, sharing only lifecycle cancellation with
// the ticket worker. A slow ticket probe cannot block Cookie acquisition.
func (s *OpenAIGatewayService) runOpenAICookieHarvester(ctx context.Context) {
	defer s.cookieHarvestRuntime.wg.Wait()
	go s.runCookieHostMonitor(ctx)
 go s.settingService.runCookieRemoteSync(ctx)
	// Restore saved bindings once at startup, independently of inference.
	if accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI); err == nil {
		for i := range accounts {
			if accounts[i].Status == StatusActive && openAICodexCookieHostFromAccount(&accounts[i]) != "" {
				if err := s.ScheduleCookieWSBinding(ctx, &accounts[i]); err != nil {
					slog.Warn("restore cookie ws binding failed", "account_id", accounts[i].ID, "error", err)
				}
			}
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		settings, err := s.settingService.GetOpenAICookieSettings(ctx)
		if err != nil {
			slog.Warn("cookie settings unavailable", "error", err)
			continue
		}
		if !settings.Enabled {
			continue
		}
		// Rotation is independent from Cookie collection account selection.
		// Check the configured rotation scope every second so an account can
		// rotate even when it is not one of the harvesting accounts.
		if settings.CookieRotationEnabled {
			s.rotateDueOpenAICookieAccounts(ctx, settings)
		}
		s.dispatchOpenAICookieHarvest(ctx, settings)
	}
}

func (s *OpenAIGatewayService) rotateDueOpenAICookieAccounts(ctx context.Context, settings *OpenAICookieSettings) {
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return
	}
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive || !isOpenAICodexTicketAccount(account) || !isOpenAICookieRotationAccount(account, settings) {
			continue
		}
		if openAICookieRotationDue(account, time.Now()) {
			go s.rotateDueOpenAICookieHost(context.Background(), account, settings)
		}
	}
}

func isOpenAICookieCollector(account *Account, settings *OpenAICookieSettings) bool {
	if cookieHostMonitorOwns(account, settings) {
		return false
	}
	if account == nil || settings == nil {
		return false
	}
	if len(settings.AccountIDs) == 0 && settings.AccountID == 0 && len(settings.GroupIDs) == 0 {
		return true
	}
	if len(settings.AccountIDs) == 0 && settings.AccountID == account.ID {
		return true
	}
	for _, id := range settings.AccountIDs {
		if id == account.ID {
			return true
		}
	}
	groupIDs := append([]int64(nil), account.GroupIDs...)
	// Collection accepts the transient degraded group in addition to explicit
	// accounts and persisted groups. Recovery/unbinding removes this extra scope.
	if target := cookieDegradedTarget(account, settings, time.Now()); target > 0 {
		groupIDs = append(groupIDs, target)
	}
	for _, accountGroup := range account.AccountGroups {
		groupIDs = append(groupIDs, accountGroup.GroupID)
	}
	for _, group := range account.Groups {
		if group != nil {
			groupIDs = append(groupIDs, group.ID)
		}
	}
	for _, groupID := range groupIDs {
		for _, id := range settings.GroupIDs {
			if id == groupID {
				return true
			}
		}
	}
	return false
}

func isExplicitOpenAICookieCollector(account *Account, settings *OpenAICookieSettings) bool {
	if account == nil || settings == nil {
		return false
	}
	selected := settings.AccountID > 0 || len(settings.AccountIDs) > 0 || len(settings.GroupIDs) > 0
	return selected && isOpenAICookieCollector(account, settings)
}

// openAICookieProbeSettings derives the per-account behavior for one harvest.
// Collection may be broad, but rotation must always honor its own scope.
func openAICookieProbeSettings(account *Account, settings *OpenAICookieSettings) *OpenAICookieSettings {
	if settings == nil {
		return nil
	}
	rotationAccount := isOpenAICookieRotationAccount(account, settings)
	explicitCollector := isExplicitOpenAICookieCollector(account, settings)
	if !explicitCollector && !(settings.CookieRotationEnabled && !rotationAccount) {
		return settings
	}
	copy := *settings
	copy.AutoValidateHost = false
	copy.autoConfigureOtherAccounts = explicitCollector && settings.CookieRotationEnabled
	if settings.CookieRotationEnabled && !rotationAccount {
		// Collection and Host rotation are separate scopes. A collector that is
		// outside the configured rotation accounts/groups may harvest a Cookie
		// for the library, but must never bind that Host to itself. Binding it
		// here would make importing an unrelated account silently opt it into
		// Host rotation.
		copy.CookieRotationEnabled = false
		copy.AutoValidateHost = false
		copy.autoConfigureOtherAccounts = false
	}
	return &copy
}
