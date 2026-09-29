package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cookieSettingsKey = "openai_cookie_settings"
const cookieLogsKey = "openai_cookie_acquisition_logs"
const intelligenceMonitorSettingKey = "openai_intelligence_monitor"

type OpenAICookieSettings struct {
	LocalHarvestDisabled bool `json:"local_harvest_disabled"`
	RemoteSyncEnabled         bool   `json:"remote_sync_enabled"`
	RemoteSyncURL             string `json:"remote_sync_url"`
	RemoteSyncAdminKey        string `json:"remote_sync_admin_key"`
	RemoteSyncIntervalSeconds int    `json:"remote_sync_interval_seconds"`

	// Runtime-only reader; never persist candidate availability in account state.
	rotationCandidates func() ([]OpenAICodexCookieLibraryEntry, error)
	DegradedGroupID    int64                `json:"degraded_group_id"`
	DegradedGroupName  string               `json:"degraded_group_name,omitempty"`
	HarvestPolicy      *CookieHarvestPolicy `json:"harvest_policy,omitempty"`
	harvestTask        string
	harvestTarget      string
	harvestProxy       string
	HostMonitor        *CookieHostMonitorConfig `json:"host_monitor,omitempty"`
	Enabled            bool                     `json:"enabled"`
	Model              string                   `json:"model"`
	IntervalSeconds    int                      `json:"interval_seconds"`
	// AccountID is retained for compatibility with older single-account
	// settings. AccountIDs is the authoritative multi-select value.
	AccountID                                  int64    `json:"account_id,omitempty"`
	AccountIDs                                 []int64  `json:"account_ids,omitempty"`
	GroupIDs                                   []int64  `json:"group_ids,omitempty"`
	RotationAccountIDs                         []int64  `json:"rotation_account_ids,omitempty"`
	RotationGroupIDs                           []int64  `json:"rotation_group_ids,omitempty"`
	CookieHostSchedulingGuardEnabled           bool     `json:"cookie_host_scheduling_guard_enabled"`
	ProxyURLs                                  []string `json:"proxy_urls"`
	ManagedProxyIDs                            []int64  `json:"managed_proxy_ids,omitempty"`
	UseAllManagedProxies                       bool     `json:"use_all_managed_proxies"`
	CookieProxyScheduleMode                    string   `json:"cookie_proxy_schedule_mode"`
	CookieHarvestConcurrency                   int      `json:"cookie_harvest_concurrency"`
	CookieProxyLearningAttempts                int      `json:"cookie_proxy_learning_attempts"`
	DynamicProxyFillHostCookie                 bool     `json:"dynamic_proxy_fill_host_cookie"`
	HostWhitelist                              []string `json:"host_whitelist"`
	AutoValidateHost                           bool     `json:"auto_validate_host"`
	WSEnabled                                  bool     `json:"ws_enabled"`
	WSConnections                              int      `json:"ws_connections"`
	WSTTLSeconds                               int      `json:"ws_ttl_seconds"`
	WSHostCooldownSeconds                      int      `json:"ws_host_cooldown_seconds"`
	CookieHostValidationFailureCooldownSeconds int      `json:"cookie_host_validation_failure_cooldown_seconds"`
	CookieRefreshBeforeSeconds                 int      `json:"cookie_refresh_before_seconds"`
	CookieRotationEnabled                      bool     `json:"cookie_rotation_enabled"`
	CookieHostBindingSeconds                   int      `json:"cookie_host_binding_seconds"`
	CookieHostRotationBeforeSeconds            int      `json:"cookie_host_rotation_before_seconds"`
	// Runtime-only fan-out marker for explicit collector accounts.
	autoConfigureOtherAccounts bool `json:"-"`
}

func validateCookieRemoteSync(v *OpenAICookieSettings) error {
	if !v.RemoteSyncEnabled {
		return nil
	}
	if strings.TrimSpace(v.RemoteSyncURL) == "" {
		return fmt.Errorf("remote_sync_url is required when remote sync is enabled")
	}
	if strings.TrimSpace(v.RemoteSyncAdminKey) == "" {
		return fmt.Errorf("remote_sync_admin_key is required when remote sync is enabled")
	}
	u, err := url.Parse(strings.TrimSpace(v.RemoteSyncURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("remote_sync_url must be an HTTP(S) server URL without credentials, query or fragment")
	}
	if v.RemoteSyncIntervalSeconds == 0 {
		return nil
	}
	if v.RemoteSyncIntervalSeconds < 10 || v.RemoteSyncIntervalSeconds > 86400 {
		return fmt.Errorf("remote_sync_interval_seconds must be between 10 and 86400")
	}
	return nil
}

type cachedOpenAICookieSettings struct {
	value OpenAICookieSettings
	until time.Time
}

// GetOpenAIIntelligenceModel reads the model configured for intelligence and
// Host capability validation. It is intentionally separate from the Cookie
// acquisition model in OpenAICookieSettings.
func (s *SettingService) GetOpenAIIntelligenceModel(ctx context.Context) string {
	const fallback = "gpt-6-astra"
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	raw, err := s.settingRepo.GetValue(ctx, intelligenceMonitorSettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return fallback
	}
	var value struct {
		ModelID string `json:"model_id"`
	}
	if json.Unmarshal([]byte(raw), &value) != nil || strings.TrimSpace(value.ModelID) == "" {
		return fallback
	}
	return strings.TrimSpace(value.ModelID)
}

var cookieSettingsMu sync.Mutex

func (s *SettingService) GetOpenAICookieSettings(ctx context.Context) (*OpenAICookieSettings, error) {
	cookieSettingsMu.Lock()
	defer cookieSettingsMu.Unlock()
	value := OpenAICookieSettings{Model: openAICodexTicketHarvestModel, IntervalSeconds: 5, CookieProxyScheduleMode: "round_robin", CookieHarvestConcurrency: 1, CookieProxyLearningAttempts: openAICookieProxyDiscoveryRequests, WSConnections: 10, WSTTLSeconds: 3600, WSHostCooldownSeconds: 14400, CookieHostValidationFailureCooldownSeconds: 120, CookieRefreshBeforeSeconds: 600, CookieHostBindingSeconds: 240, CookieHostRotationBeforeSeconds: 10, ProxyURLs: []string{}, HostWhitelist: []string{}}
	if s == nil || s.settingRepo == nil {
		return &value, nil
	}
	if cached, ok := s.openAICookieCache.Load().(*cachedOpenAICookieSettings); ok && time.Now().Before(cached.until) {
		copy := cached.value
		copy.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return s.cookieSchedulingCandidates(ctx) }
		return &copy, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, cookieSettingsKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		// Migrate legacy configuration once on first save; ticket settings never
		// write this key, so the two features remain independent afterwards.
		old, err := s.GetOpenAICodexTicketSettings(ctx)
		if err != nil {
			return nil, err
		}
		value.Enabled = old.Enabled && len(old.CookieHarvestProxyURLs) > 0
		value.Model, value.IntervalSeconds = old.Model, old.RetryIntervalSeconds
		value.ProxyURLs, value.HostWhitelist = old.CookieHarvestProxyURLs, old.CookieHostWhitelist
		if len(value.ProxyURLs) == 0 && old.HarvestProxyURL != "" {
			value.ProxyURLs = []string{old.HarvestProxyURL}
		}
		value.WSEnabled = true // preserve existing bound-account transport on migration
		value.WSConnections, value.WSTTLSeconds, value.WSHostCooldownSeconds = old.CookieWSConnections, old.CookieWSConnectionTTLSeconds, old.CookieWSHostCooldownSeconds
		value.CookieRefreshBeforeSeconds = 600
		value.CookieHostBindingSeconds = 240
		value.CookieHostRotationBeforeSeconds = 10
		if value.WSTTLSeconds > 3600 {
			value.WSTTLSeconds = 3600
		}
		if err := s.setOpenAICookieSettings(ctx, &value); err != nil {
			return nil, err
		}
	} else if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("decode cookie settings: %w", err)
	}
	if value.CookieHostValidationFailureCooldownSeconds <= 0 {
		value.CookieHostValidationFailureCooldownSeconds = 120
	}
	if value.CookieHostBindingSeconds <= 0 {
		value.CookieHostBindingSeconds = 240
	}
	if value.CookieHostRotationBeforeSeconds < 0 {
		value.CookieHostRotationBeforeSeconds = 10
	}
	if strings.TrimSpace(value.CookieProxyScheduleMode) == "" {
		if value.DynamicProxyFillHostCookie {
			value.CookieProxyScheduleMode = "dynamic"
		} else {
			value.CookieProxyScheduleMode = "round_robin"
		}
	}
	if value.CookieHarvestConcurrency <= 0 {
		value.CookieHarvestConcurrency = 1
	}
	if value.CookieProxyLearningAttempts <= 0 {
		value.CookieProxyLearningAttempts = openAICookieProxyDiscoveryRequests
	}
	if value.HarvestPolicy == nil {
		value.HarvestPolicy = defaultCookieHarvestPolicy()
	}
	if value.DegradedGroupID > 0 && s.defaultSubGroupReader != nil {
		if group, err := s.defaultSubGroupReader.GetByID(ctx, value.DegradedGroupID); err == nil && group != nil {
			value.DegradedGroupName = group.Name
		}
	}
	// Scheduling policy changes must become visible quickly after an admin
	// update; keep this cache short while still avoiding a settings DB read per
	// request.
	s.openAICookieCache.Store(&cachedOpenAICookieSettings{value, time.Now().Add(1 * time.Second)})
	value.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return s.cookieSchedulingCandidates(ctx) }
	return &value, nil
}

func (s *SettingService) SetOpenAICookieSettings(ctx context.Context, value *OpenAICookieSettings) error {
	cookieSettingsMu.Lock()
	defer cookieSettingsMu.Unlock()
	// The dedicated monitor endpoint owns its configuration. A stale Cookie
	// settings form must not reset an active experiment or its identity.
	if s != nil && s.settingRepo != nil && value != nil {
		raw, err := s.settingRepo.GetValue(ctx, cookieSettingsKey)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return err
		}
		var old OpenAICookieSettings
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &old); err != nil {
				return err
			}
		}
		value.HostMonitor = old.HostMonitor
		if value.RemoteSyncAdminKey == "" {
			value.RemoteSyncAdminKey = old.RemoteSyncAdminKey
		}
	}
	return s.setOpenAICookieSettings(ctx, value)
}

func (s *SettingService) setOpenAICookieSettings(ctx context.Context, value *OpenAICookieSettings) error {
	if s == nil || s.settingRepo == nil || value == nil {
		return fmt.Errorf("cookie settings unavailable")
	}
	if value.HarvestPolicy == nil {
		value.HarvestPolicy = defaultCookieHarvestPolicy()
	}
	if err := value.HarvestPolicy.validate(); err != nil {
		return err
	}
	if value.DegradedGroupID < 0 {
		return fmt.Errorf("降级分组配置无效")
	}
	value.DegradedGroupName = ""
	if value.DegradedGroupID > 0 {
		for _, id := range value.RotationGroupIDs {
			if id == value.DegradedGroupID {
				return fmt.Errorf("降级分组不能同时作为 Host 轮换来源分组")
			}
		}
		if s.defaultSubGroupReader == nil {
			return fmt.Errorf("分组校验服务不可用")
		}
		group, err := s.defaultSubGroupReader.GetByID(ctx, value.DegradedGroupID)
		if err != nil || group == nil || !group.IsActive() || group.Platform != PlatformOpenAI {
			return fmt.Errorf("降级分组必须是启用的 OpenAI 分组")
		}
		value.DegradedGroupName = group.Name
	}
	if err := validateCookieRemoteSync(value); err != nil {
		return err
	}
	value.Model = strings.TrimSpace(value.Model)
	if value.Model == "" || len(value.Model) > 128 {
		return fmt.Errorf("model must contain 1-128 characters")
	}
	if value.IntervalSeconds < 1 || value.IntervalSeconds > 3600 {
		return fmt.Errorf("interval_seconds must be between 1 and 3600")
	}
	value.CookieProxyScheduleMode = strings.TrimSpace(strings.ToLower(value.CookieProxyScheduleMode))
	if value.CookieProxyScheduleMode != "round_robin" && value.CookieProxyScheduleMode != "dynamic" {
		return fmt.Errorf("cookie_proxy_schedule_mode must be round_robin or dynamic")
	}
	if value.CookieHarvestConcurrency < 1 || value.CookieHarvestConcurrency > 64 {
		return fmt.Errorf("cookie_harvest_concurrency must be between 1 and 64")
	}
	if value.CookieProxyLearningAttempts < 1 || value.CookieProxyLearningAttempts > 10000 {
		return fmt.Errorf("cookie_proxy_learning_attempts must be between 1 and 10000")
	}
	if value.AccountID < 0 {
		return fmt.Errorf("account_id must be nonnegative")
	}
	for _, id := range value.AccountIDs {
		if id < 0 {
			return fmt.Errorf("account_ids must be nonnegative")
		}
	}
	for _, id := range value.GroupIDs {
		if id < 0 {
			return fmt.Errorf("group_ids must be nonnegative")
		}
	}
	for _, id := range value.RotationAccountIDs {
		if id < 0 {
			return fmt.Errorf("rotation_account_ids must be nonnegative")
		}
	}
	for _, id := range value.RotationGroupIDs {
		if id < 0 {
			return fmt.Errorf("rotation_group_ids must be nonnegative")
		}
	}
	for _, id := range value.ManagedProxyIDs {
		if id < 0 {
			return fmt.Errorf("managed_proxy_ids must be nonnegative")
		}
	}
	if value.AccountIDs == nil && value.AccountID > 0 {
		value.AccountIDs = []int64{value.AccountID}
	}
	value.AccountIDs = normalizeCookieAccountIDs(value.AccountIDs)
	value.GroupIDs = normalizeCookieAccountIDs(value.GroupIDs)
	value.RotationAccountIDs = normalizeCookieAccountIDs(value.RotationAccountIDs)
	value.RotationGroupIDs = normalizeCookieAccountIDs(value.RotationGroupIDs)
	value.ManagedProxyIDs = normalizeCookieAccountIDs(value.ManagedProxyIDs)
	value.AccountID = 0
	if len(value.AccountIDs) > 0 {
		value.AccountID = value.AccountIDs[0]
	}
	if value.WSConnections < 1 || value.WSConnections > 64 {
		return fmt.Errorf("ws_connections must be between 1 and 64")
	}
	if value.WSTTLSeconds < 60 || value.WSTTLSeconds > 3600 {
		return fmt.Errorf("ws_ttl_seconds must be between 60 and 3600")
	}
	if value.WSHostCooldownSeconds < 0 || value.WSHostCooldownSeconds > 604800 {
		return fmt.Errorf("invalid host cooldown")
	}
	if value.CookieHostValidationFailureCooldownSeconds == 0 {
		value.CookieHostValidationFailureCooldownSeconds = 120
	}
	if value.CookieHostValidationFailureCooldownSeconds < 1 || value.CookieHostValidationFailureCooldownSeconds > 604800 {
		return fmt.Errorf("cookie_host_validation_failure_cooldown_seconds must be between 1 and 604800")
	}
	if value.CookieRefreshBeforeSeconds < 0 || value.CookieRefreshBeforeSeconds > 86400 {
		return fmt.Errorf("cookie_refresh_before_seconds must be between 0 and 86400")
	}
	if value.CookieRotationEnabled && value.WSEnabled {
		return fmt.Errorf("cookie_rotation_enabled and ws_enabled are mutually exclusive")
	}
	if value.CookieHostBindingSeconds < 10 || value.CookieHostBindingSeconds > 86400 {
		return fmt.Errorf("cookie_host_binding_seconds must be between 10 and 86400")
	}
	if value.CookieHostRotationBeforeSeconds < 0 || value.CookieHostRotationBeforeSeconds > value.CookieHostBindingSeconds {
		return fmt.Errorf("cookie_host_rotation_before_seconds must be between 0 and binding seconds")
	}
	for _, proxy := range value.ProxyURLs {
		u, err := url.Parse(strings.TrimSpace(proxy))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return fmt.Errorf("invalid proxy URL")
		}
	}
	value.ProxyURLs = normalizeOpenAICodexProxyURLs(value.ProxyURLs)
	value.HostWhitelist = normalizeOpenAICodexCookieHostWhitelist(value.HostWhitelist)
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := s.settingRepo.Set(ctx, cookieSettingsKey, string(data)); err != nil {
		return err
	}
	s.openAICookieCache.Store(&cachedOpenAICookieSettings{*value, time.Now().Add(1 * time.Second)})
	return nil
}

func normalizeCookieAccountIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (s *SettingService) resolveOpenAICookieProxyURLs(ctx context.Context, settings *OpenAICookieSettings) []string {
	if settings == nil {
		return nil
	}
	proxyURLs := append([]string(nil), settings.ProxyURLs...)
	if (!settings.UseAllManagedProxies && len(settings.ManagedProxyIDs) == 0) || s == nil || s.proxyRepo == nil {
		return normalizeOpenAICodexProxyURLs(proxyURLs)
	}
	var managed []Proxy
	var err error
	if settings.UseAllManagedProxies {
		managed, err = s.proxyRepo.ListActive(ctx)
	} else {
		managed, err = s.proxyRepo.ListByIDs(ctx, settings.ManagedProxyIDs)
	}
	if err != nil {
		return normalizeOpenAICodexProxyURLs(proxyURLs)
	}
	now := time.Now()
	for i := range managed {
		if !managed[i].IsActive() || managed[i].IsExpired(now) {
			continue
		}
		proxyURLs = append(proxyURLs, managed[i].URL())
	}
	return normalizeOpenAICodexProxyURLs(proxyURLs)
}

type OpenAICookieAcquisitionLog struct {
	harvestAttempted   bool
	Task               string         `json:"task,omitempty"`
	TargetHost         string         `json:"target_host,omitempty"`
	ID                 string         `json:"id"`
	AttemptID          string         `json:"attempt_id,omitempty"`
	Stage              string         `json:"stage,omitempty"`
	AccountID          int64          `json:"account_id"`
	AccountName        string         `json:"account_name"`
	CreatedAt          time.Time      `json:"created_at"`
	Model              string         `json:"model"`
	Proxy              string         `json:"proxy"`
	ProxyUsername      string         `json:"proxy_username,omitempty"`
	StatusCode         int            `json:"status_code"`
	Success            bool           `json:"success"`
	Message            string         `json:"message"`
	Host               string         `json:"host"`
	Cookie             string         `json:"cookie"`
	Payload            map[string]any `json:"payload,omitempty"`
	SessionID          string         `json:"session_id"`
	Response           string         `json:"response"`
	ValidationEnabled  bool           `json:"validation_enabled,omitempty"`
	ValidationResult   string         `json:"validation_result,omitempty"`
	ValidationResponse string         `json:"validation_response,omitempty"`
	BindingStatus      string         `json:"binding_status,omitempty"`
	BindingHost        string         `json:"binding_host,omitempty"`
	Kind               string         `json:"kind,omitempty"`
}

var cookieLogMu sync.Mutex

type OpenAICookieBindingStatus struct {
	DegradedGroupID         int64                `json:"degraded_group_id,omitempty"`
	DegradedGroupName       string               `json:"degraded_group_name,omitempty"`
	SchedulingGuardEnabled  bool                 `json:"scheduling_guard_enabled"`
	SchedulingBlocked       bool                 `json:"scheduling_blocked"`
	SchedulingBlockReason   string               `json:"scheduling_block_reason,omitempty"`
	Host                    string               `json:"host"`
	Status                  string               `json:"status"`
	ExpiresAt               *time.Time           `json:"expires_at,omitempty"`
	BindingExpiresAt        *time.Time           `json:"binding_expires_at,omitempty"`
	RotationAt              *time.Time           `json:"rotation_at,omitempty"`
	CookieExpiresAt         *time.Time           `json:"cookie_expires_at,omitempty"`
	AvailableHostCount      int                  `json:"available_host_count"`
	BindingSeconds          int                  `json:"binding_seconds"`
	RotationBeforeSeconds   int                  `json:"rotation_before_seconds"`
	EstimatedBindingSeconds *int64               `json:"estimated_binding_seconds,omitempty"`
	WSEnabled               bool                 `json:"ws_enabled"`
	CooldownHost            string               `json:"cooldown_host,omitempty"`
	CooldownUntil           *time.Time           `json:"cooldown_until,omitempty"`
	Cooldowns               map[string]time.Time `json:"cooldowns,omitempty"`
	RotationStatus          string               `json:"rotation_status,omitempty"`
	RotationStartedAt       *time.Time           `json:"rotation_started_at,omitempty"`
	RotationMessage         string               `json:"rotation_message,omitempty"`
}

func (s *SettingService) OpenAICookieBinding(ctx context.Context, account *Account) *OpenAICookieBindingStatus {
	if !isOpenAICodexTicketAccount(account) {
		return nil
	}
	status := &OpenAICookieBindingStatus{Host: openAICodexCookieHostFromAccount(account), Status: "unbound"}
	status.SchedulingGuardEnabled, status.SchedulingBlockReason = s.openAICookieSchedulingStatus(ctx, account)
	status.SchedulingBlocked = status.SchedulingBlockReason != ""
	if account != nil {
		status.RotationStatus = strings.TrimSpace(account.GetExtraString(openAICodexCookieRotationStatusExtraKey))
		status.RotationMessage = strings.TrimSpace(account.GetExtraString(openAICodexCookieRotationMessageExtraKey))
		if raw := strings.TrimSpace(account.GetExtraString(openAICodexCookieRotationStartedExtraKey)); raw != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
				status.RotationStartedAt = &parsed
			}
		}
		status.Cooldowns = openAICodexCookieHostCooldowns(account)
		if until, ok := status.Cooldowns[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(status.Host), "."))]; ok && until.After(time.Now()) {
			status.CooldownHost = status.Host
			status.CooldownUntil = &until
			if status.Host == "" {
				status.Status = "cooldown"
			}
		}
	}
	rotationEligible := false
	settingsLoaded := false
	if cfg, err := s.GetOpenAICookieSettings(ctx); err == nil {
		settingsLoaded = true
		status.BindingSeconds = cfg.CookieHostBindingSeconds
		status.RotationBeforeSeconds = cfg.CookieHostRotationBeforeSeconds
		if cookieDegradedTarget(account, cfg, time.Now()) > 0 {
			status.DegradedGroupID = cfg.DegradedGroupID
			status.DegradedGroupName = cfg.DegradedGroupName
			status.SchedulingBlocked = false
		}
		status.WSEnabled = cfg.WSEnabled
		rotationEligible = cfg.CookieRotationEnabled && isOpenAICookieRotationAccount(account, cfg)
	}
	if raw := strings.TrimSpace(account.GetExtraString("codex_cookie_host_binding_expires_at")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			status.BindingExpiresAt = &parsed
		}
	}
	if raw := strings.TrimSpace(account.GetExtraString("codex_cookie_host_rotation_at")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			status.RotationAt = &parsed
		}
	}
	// Rotation metadata is persisted on the account so an in-flight task can
	// survive refreshes. Do not expose stale metadata once the account is no
	// longer in the configured rotation scope (or rotation is disabled).
	if !rotationEligible {
		status.RotationAt = nil
		status.RotationStatus = ""
		status.RotationStartedAt = nil
		status.RotationMessage = ""
	}
	if entries, err := s.GetOpenAICodexCookieLibrary(ctx); err == nil {
		now := time.Now()
		for _, item := range entries {
			if openAICookieRotationCandidate(account, item, now) {
				status.AvailableHostCount++
			}
		}
		if settingsLoaded {
			secondsPerHost := max(0, status.BindingSeconds-status.RotationBeforeSeconds)
			estimate := int64(status.AvailableHostCount) * int64(secondsPerHost)
			status.EstimatedBindingSeconds = &estimate
		}
	}
	if status.Host == "" {
		// An unbound account may retain old deadline/rotation fields after a
		// failed replacement. They are historical metadata, never an active
		// binding, and must not be exposed as a live countdown.
		status.BindingExpiresAt = nil
		status.RotationAt = nil
		status.ExpiresAt = nil
		status.CookieExpiresAt = nil
		status.RotationStatus = ""
		status.RotationStartedAt = nil
		status.RotationMessage = ""
		return status
	}
	status.Status = "expired"
	entry, err := s.LookupOpenAICodexCookie(ctx, status.Host)
	if err != nil {
		status.Status = "unavailable"
	} else if entry != nil {
		status.Status = "active"
		status.ExpiresAt = &entry.ExpiresAt
		status.CookieExpiresAt = &entry.ExpiresAt
	}
	return status
}

func openAICodexCookieHostCooldowns(account *Account) map[string]time.Time {
	result := make(map[string]time.Time)
	if account == nil || account.Extra == nil {
		return result
	}
	if raw, ok := account.Extra[openAICodexCookieCooldownsExtraKey]; ok {
		if data, err := json.Marshal(raw); err == nil {
			var values map[string]string
			if json.Unmarshal(data, &values) == nil {
				for host, value := range values {
					if until, err := time.Parse(time.RFC3339Nano, value); err == nil && until.After(time.Now()) {
						result[normalizeOpenAICookieHost(host)] = until
					}
				}
			}
		}
	}
	// Legacy scalar records are exposed until they expire, unless the same
	// account already has an active WS runtime marker (where the old field was
	// also reused for the active host).
	legacyHost := normalizeOpenAICookieHost(account.GetExtraString("codex_cookie_ws_host"))
	if legacyHost != "" && strings.TrimSpace(account.GetExtraString("codex_cookie_ws_started_at")) == "" {
		if until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(account.GetExtraString("codex_cookie_host_cooldown_until"))); err == nil && until.After(time.Now()) {
			result[legacyHost] = until
		}
	}
	return result
}

func (s *SettingService) GetOpenAICookieLogs(ctx context.Context) ([]OpenAICookieAcquisitionLog, error) {
	return s.getOpenAICookieLogs(ctx, cookieLogsKey)
}

func (s *SettingService) GetOpenAICookieValidationLogs(ctx context.Context) ([]OpenAICookieAcquisitionLog, error) {
	return s.getOpenAICookieLogs(ctx, "openai_cookie_validation_logs")
}

// GetOpenAICookieValidationLogsPage paginates validation attempts rather than
// individual stage rows, so every returned attempt contains its full timeline.
func (s *SettingService) GetOpenAICookieValidationLogsPage(ctx context.Context, accountID int64, page, pageSize int, hosts ...string) ([]OpenAICookieAcquisitionLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	if repo, ok := s.settingRepo.(CookieValidationHistoryRepository); ok {
		host := ""
		if len(hosts) > 0 {
			host = normalizeOpenAICookieHost(hosts[0])
		}
		return repo.CookieValidationPage(ctx, accountID, host, page, pageSize)
	}
	logs, err := s.GetOpenAICookieValidationLogs(ctx)
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	filtered := make([]OpenAICookieAcquisitionLog, 0, len(logs))
	attemptOrder := make([]string, 0)
	seenAttempts := make(map[string]struct{})
	for _, item := range logs {
		if accountID > 0 && item.AccountID != accountID {
			continue
		}
		attemptID := strings.TrimSpace(item.AttemptID)
		if attemptID == "" {
			attemptID = item.ID
		}
		filtered = append(filtered, item)
		if _, ok := seenAttempts[attemptID]; !ok {
			seenAttempts[attemptID] = struct{}{}
			attemptOrder = append(attemptOrder, attemptID)
		}
	}

	total := int64(len(attemptOrder))
	start := (page - 1) * pageSize
	if start >= len(attemptOrder) {
		return []OpenAICookieAcquisitionLog{}, total, nil
	}
	end := start + pageSize
	if end > len(attemptOrder) {
		end = len(attemptOrder)
	}
	pageAttempts := make(map[string]struct{}, end-start)
	for _, attemptID := range attemptOrder[start:end] {
		pageAttempts[attemptID] = struct{}{}
	}
	items := make([]OpenAICookieAcquisitionLog, 0)
	for _, item := range filtered {
		attemptID := strings.TrimSpace(item.AttemptID)
		if attemptID == "" {
			attemptID = item.ID
		}
		if _, ok := pageAttempts[attemptID]; ok {
			items = append(items, item)
		}
	}
	return items, total, nil
}

func (s *SettingService) getOpenAICookieLogs(ctx context.Context, key string) ([]OpenAICookieAcquisitionLog, error) {
	logs := []OpenAICookieAcquisitionLog{}
	if s == nil || s.settingRepo == nil {
		return logs, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, key)
	if errors.Is(err, ErrSettingNotFound) {
		return logs, nil
	}
	if err != nil {
		return nil, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &logs); err != nil {
			return nil, err
		}
	}
	return logs, nil
}

func (s *SettingService) appendOpenAICookieLog(ctx context.Context, item OpenAICookieAcquisitionLog) error {
	if item.Kind == "validation" {
		if item.BindingStatus == "rotation_waiting" || item.BindingStatus == "rotation_started" || item.BindingStatus == "rotation_candidate" {
			return nil
		}
		if repo, ok := s.settingRepo.(CookieValidationHistoryRepository); ok {
			return repo.AppendCookieValidation(ctx, item)
		}
	}
	cookieLogMu.Lock()
	defer cookieLogMu.Unlock()
	key := cookieLogsKey
	if item.Kind == "validation" {
		key = "openai_cookie_validation_logs"
	}
	logs, err := s.getOpenAICookieLogs(ctx, key)
	if err != nil {
		return err
	}
	if item.Kind == "scheduler" && len(logs) > 0 && logs[0].Kind == "scheduler" && logs[0].Message == item.Message {
		return nil
	}
	if len(item.Response) > 32768 {
		item.Response = item.Response[:32768] + "\n[响应超过 32 KiB，已截断]"
	}
	logs = append([]OpenAICookieAcquisitionLog{item}, logs...)
	limit := 200
	if item.Kind == "validation" {
		limit = 1000
	}
	if len(logs) > limit {
		logs = logs[:limit]
	}
	data, err := json.Marshal(logs)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, key, string(data))
}
