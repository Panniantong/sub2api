package service

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrUnknownAntiDegradeMode = infraerrors.BadRequest("UNKNOWN_PROTECTION_STRATEGY", "未知的账号保护策略")

// Account protection configures stable identity, isolated conversations,
// bounded concurrency and protocol-aware request integrity. Mode 1 v3 uses
// the original standard transport. These settings cannot prove model quality.
// Preview is read-only; Apply preserves a snapshot for explicit Revert.
const (
	// AntiDegradeMarkerExtraKey 一键防降智标记与快照。
	AntiDegradeMarkerExtraKey = "anti_degrade"
	// AntiDegradeConcurrencyCap 无限制并发一键收敛的目标值。
	AntiDegradeConcurrencyCap = 16
)

// AntiDegradeMode selects the current compatibility policy or the legacy policy.
type AntiDegradeMode string

const (
	AntiDegradeMode1       AntiDegradeMode = "mode1"
	AntiDegradeMode2       AntiDegradeMode = "mode2"
	AntiDegradeModeNative  AntiDegradeMode = "native_baseline"
	AntiDegradeModeMinimal AntiDegradeMode = "minimal_compat"
	AntiDegradeModeSession AntiDegradeMode = "session_standard"
	// AntiDegradeModeLegacy is the original (sub2初代) strategy: session
	// fingerprint convergence with the nodejs24 TLS template.  It is kept as
	// an explicit option so operators can reproduce the known-good baseline.
	AntiDegradeModeLegacy         AntiDegradeMode = "legacy"
	AntiDegradeModeTLSNode24      AntiDegradeMode = "tls_node24"
	AntiDegradeModeLowConcurrency AntiDegradeMode = "low_concurrency"
	// DefaultAntiDegradeMode applies only when selecting a new policy. Existing
	// persisted policies must keep their original interpretation.
	DefaultAntiDegradeMode AntiDegradeMode = AntiDegradeModeLegacy
)

func normalizeAntiDegradeMode(mode AntiDegradeMode) AntiDegradeMode {
	if mode == "" {
		return DefaultAntiDegradeMode
	}
	return mode
}

func antiDegradeModeSettings(mode AntiDegradeMode) (codexFingerprintMode, string, int) {
	profile := antiDegradeStrategyProfile(normalizeAntiDegradeMode(mode))
	if profile.ID == AntiDegradeModeNative {
		return codexFingerprintOff, "", 0
	}
	fingerprint := codexFingerprintMode(profile.IdentityMode)
	if fingerprint == "" {
		fingerprint = codexFingerprintOff
	}
	return fingerprint, profile.TLSProfile, profile.MaxConcurrency
}

// AntiDegradeChange 单项改动（展示用）。
type AntiDegradeChange struct {
	Key  string `json:"key"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to"`
	Note string `json:"note,omitempty"`
}

// AntiDegradePreview 预览结果。
type AntiDegradePreview struct {
	Runtime       *ProtectionRuntimeState `json:"runtime,omitempty"`
	ActiveMode    string                  `json:"active_mode"`
	PolicyVersion int                     `json:"policy_version"`
	IdentityReady bool                    `json:"identity_ready"`
	TLSProfile    string                  `json:"tls_profile"`
	Issues        []string                `json:"issues"`
	AccountID     int64                   `json:"account_id"`
	Enabled       bool                    `json:"enabled"`
	Eligible      bool                    `json:"eligible"`
	Reason        string                  `json:"reason,omitempty"`
	Changes       []AntiDegradeChange     `json:"changes"`
}

// AntiDegradeStore 防降智需要的账号读写接口（AdminService 已实现，无需改接口）。
type AntiDegradeStore interface {
	GetAccount(ctx context.Context, id int64) (*Account, error)
	UpdateAccount(ctx context.Context, id int64, input *UpdateAccountInput) (*Account, error)
}

// AntiDegradeService 一键防降智服务（无状态，可直接构造）。
type AntiDegradeService struct {
	admin         AntiDegradeStore
	cfg           *config.Config
	pluginManager *PluginManager
}

// NewAntiDegradeService 创建防降智服务。
func NewAntiDegradeService(admin AntiDegradeStore) *AntiDegradeService {
	return &AntiDegradeService{admin: admin}
}

func isOpenAIOAuthLike(a *Account) bool {
	return a != nil && a.Platform == PlatformOpenAI &&
		(a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken)
}

func isAnthropicOAuthLike(a *Account) bool {
	return a != nil && a.Platform == PlatformAnthropic &&
		(a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken)
}

// antiDegradeEnabled 是否已启用一键防降智。
func antiDegradeEnabled(a *Account) bool {
	if a == nil || len(a.Extra) == 0 {
		return false
	}
	marker, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := marker["enabled"].(bool)
	return enabled
}

func antiDegradePrev(a *Account) map[string]any {
	if a == nil || len(a.Extra) == 0 {
		return nil
	}
	marker, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	if !ok {
		return nil
	}
	prev, _ := marker["prev"].(map[string]any)
	return prev
}

func antiDegradeMode(a *Account) AntiDegradeMode {
	if a != nil && a.Extra != nil {
		if marker, ok := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any); ok {
			if raw, ok := marker["mode"].(string); ok && raw != "" {
				return AntiDegradeMode(raw)
			}
		}
	}
	return AntiDegradeMode1
}

// PreviewAntiDegrade 计算改动集（不写库）。
func PreviewAntiDegrade(account *Account) AntiDegradePreview {
	return PreviewAntiDegradeMode(account, DefaultAntiDegradeMode)
}

// PreviewAntiDegradeMode 计算指定方案的真实改动集（不写库）。
func PreviewAntiDegradeMode(account *Account, mode AntiDegradeMode) AntiDegradePreview {
	mode = normalizeAntiDegradeMode(mode)
	if antiDegradeStrategyProfile(mode).ID == "" {
		return AntiDegradePreview{Changes: []AntiDegradeChange{}, Reason: "未知的账号保护策略"}
	}
	if mode == AntiDegradeModeNative {
		out := AntiDegradePreview{Changes: []AntiDegradeChange{}, TLSProfile: "account"}
		if account == nil {
			out.Reason = "account not found"
			return out
		}
		out.AccountID = account.ID
		out.Enabled = antiDegradeEnabled(account)
		if out.Enabled {
			out.ActiveMode = string(antiDegradeMode(account))
			out.Changes = append(out.Changes, AntiDegradeChange{Key: "strategy", From: string(antiDegradeMode(account)), To: string(AntiDegradeModeNative), Note: "切换为原生基线；需要管理员确认关闭保护"})
			out.Eligible = true
			out.Reason = "原生基线不会写入新的保护配置"
		} else {
			out.Reason = "当前已是原生基线"
		}
		return out
	}
	if mode == AntiDegradeMode1 {
		return previewMode1(account)
	}
	out := AntiDegradePreview{Changes: []AntiDegradeChange{}}
	if account == nil {
		out.Reason = "account not found"
		return out
	}
	out.AccountID = account.ID
	out.Enabled = antiDegradeEnabled(account)
	if reason := antiDegradeEligibilityIssue(account, mode); reason != "" {
		out.Reason = reason
		return out
	}
	if out.Enabled {
		out.ActiveMode = string(antiDegradeMode(account))
	}
	// Surface the effective baseline values in previews as well as the change
	// list.  This lets the admin UI show which strategy is actually active
	// instead of falling back to an "unverified" badge after a modal refresh.
	if _, ok := codexFingerprintSeed(account.Extra); ok {
		out.IdentityReady = true
	}
	effectiveMode := mode
	if out.Enabled {
		effectiveMode = antiDegradeMode(account)
	}
	_, effectiveTLS, _ := antiDegradeModeSettings(effectiveMode)
	if (isOpenAIOAuthLike(account) || isAnthropicOAuthLike(account)) && effectiveTLS != "" {
		out.TLSProfile = effectiveTLS
	}
	if out.Enabled {
		if antiDegradeMode(account) == mode {
			out.Reason = "already enabled, can revert"
			return out
		}
		out.Eligible = true
		out.Reason = "可切换到所选防降智策略"
		out.Changes = append(out.Changes, AntiDegradeChange{Key: "strategy", From: string(antiDegradeMode(account)), To: string(mode), Note: "先安全还原当前快照，再应用所选策略"})
		return out
	}
	targetFingerprint, targetTLS, targetConcurrency := antiDegradeModeSettings(mode)
	if isOpenAIOAuthLike(account) {
		if account.GetCodexFingerprintMode() != targetFingerprint {
			out.Changes = append(out.Changes, AntiDegradeChange{
				Key:  "extra.codex_fingerprint_mode",
				From: string(account.GetCodexFingerprintMode()),
				To:   string(targetFingerprint),
				Note: "账号级 Codex 身份收敛",
			})
		}
		if targetTLS == "standard" {
			if account.IsTLSFingerprintEnabled() || account.Extra["tls_fingerprint_builtin"] != nil || account.Extra["tls_fingerprint_profile_id"] != nil {
				out.Changes = append(out.Changes, AntiDegradeChange{Key: "transport", From: account.Extra["tls_fingerprint_builtin"], To: targetTLS, Note: "恢复标准 TLS 传输"})
			}
		} else if !account.IsTLSFingerprintEnabled() || account.Extra["tls_fingerprint_builtin"] != targetTLS {
			out.Changes = append(out.Changes, AntiDegradeChange{Key: "extra.tls_fingerprint_builtin", From: account.Extra["tls_fingerprint_builtin"], To: targetTLS, Note: "完整 TLS ClientHello 模板"})
		}
	} else if isAnthropicOAuthLike(account) {
		if targetTLS != "standard" && (!account.IsTLSFingerprintEnabled() || account.Extra["tls_fingerprint_builtin"] != targetTLS) {
			out.Changes = append(out.Changes, AntiDegradeChange{Key: "extra.tls_fingerprint_builtin", From: account.Extra["tls_fingerprint_builtin"], To: targetTLS, Note: "完整 TLS ClientHello 模板"})
		}
	} else {
		out.Reason = "platform has no fingerprint convergence; only generic items apply"
	}
	if account.Concurrency <= 0 {
		out.Changes = append(out.Changes, AntiDegradeChange{
			Key:  "concurrency",
			From: account.Concurrency,
			To:   targetConcurrency,
			Note: "不限流并发收敛，避免突发触发上游限流",
		})
	}
	out.Eligible = len(out.Changes) > 0
	// Selecting a strategy is itself a meaningful change even when the
	// account's low-level fields already happen to match the target values.
	// Persisting the marker makes the effective mode explicit and allows the
	// runtime/UI to distinguish legacy/mode2 from an unconfigured account.
	if !out.Eligible && !out.Enabled {
		out.Eligible = true
		out.Changes = append(out.Changes, AntiDegradeChange{
			Key: "strategy", From: string(antiDegradeMode(account)), To: string(mode),
			Note: "记录所选防降智策略",
		})
	}
	if !out.Eligible && out.Reason == "" {
		out.Reason = "nothing to change"
	}
	return out
}

// Preview 获取账号并计算改动预览（不写库，供 handler 用）。
func (s *AntiDegradeService) Preview(ctx context.Context, id int64) (AntiDegradePreview, error) {
	return s.PreviewMode(ctx, id, DefaultAntiDegradeMode)
}

func (s *AntiDegradeService) PreviewMode(ctx context.Context, id int64, mode AntiDegradeMode) (AntiDegradePreview, error) {
	if s == nil || s.admin == nil {
		return AntiDegradePreview{}, errors.New("anti-degrade service unavailable")
	}
	mode = normalizeAntiDegradeMode(mode)
	if antiDegradeStrategyProfile(mode).ID == "" {
		return AntiDegradePreview{}, ErrUnknownAntiDegradeMode
	}
	account, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return AntiDegradePreview{}, err
	}
	preview := PreviewAntiDegradeMode(account, mode)
	preview.Runtime = ResolveProtectionRuntime(account, s.cfg, s.pluginManager)
	return preview, nil
}

// Apply handler 适配：应用一键防降智。
func (s *AntiDegradeService) Apply(ctx context.Context, id int64) (*Account, error) {
	return s.ApplyAntiDegradeMode(ctx, id, DefaultAntiDegradeMode)
}

// Revert handler 适配：一键还原。
func (s *AntiDegradeService) Revert(ctx context.Context, id int64) (*Account, error) {
	return s.RevertAntiDegrade(ctx, id)
}

// ApplyAntiDegrade 应用一键防降智并快照旧值。
func (s *AntiDegradeService) ApplyAntiDegrade(ctx context.Context, id int64) (*Account, error) {
	return s.ApplyAntiDegradeMode(ctx, id, DefaultAntiDegradeMode)
}

// ApplyAntiDegradeMode 应用指定方案并快照旧值。
func (s *AntiDegradeService) ApplyAntiDegradeMode(ctx context.Context, id int64, mode AntiDegradeMode) (*Account, error) {
	return s.transition(ctx, id, func(planner *AntiDegradeService) error {
		_, err := planner.applyAntiDegradeMode(ctx, id, mode)
		return err
	})
}

func (s *AntiDegradeService) applyAntiDegradeMode(ctx context.Context, id int64, mode AntiDegradeMode) (*Account, error) {
	if s == nil || s.admin == nil {
		return nil, errors.New("anti-degrade service unavailable")
	}
	mode = normalizeAntiDegradeMode(mode)
	if antiDegradeStrategyProfile(mode).ID == "" {
		return nil, ErrUnknownAntiDegradeMode
	}
	account, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if mode == AntiDegradeModeNative {
		return nil, errors.New("native baseline requires the explicit protection revert confirmation")
	}
	if reason := antiDegradeEligibilityIssue(account, mode); reason != "" {
		return nil, infraerrors.BadRequest("PROTECTION_NOT_ELIGIBLE", reason)
	}
	// This service operates on the transition's in-memory draft. Only its
	// final state is committed by transition().
	if antiDegradeEnabled(account) && antiDegradeMode(account) != mode {
		if _, err := s.revertAntiDegrade(ctx, id); err != nil {
			return nil, err
		}
		account, err = s.admin.GetAccount(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	if mode == AntiDegradeMode1 {
		return s.applyMode1(ctx, account)
	}
	preview := PreviewAntiDegradeMode(account, mode)
	if preview.Enabled {
		return account, nil
	}
	if !preview.Eligible {
		return account, nil
	}
	extra := maps.Clone(account.Extra)
	if extra == nil {
		extra = make(map[string]any)
	}
	prev := snapshotAntiDegradeConfig(account)
	input := &UpdateAccountInput{Extra: extra}
	targetFingerprint, targetTLS, targetConcurrency := antiDegradeModeSettings(mode)
	if isOpenAIOAuthLike(account) && account.GetCodexFingerprintMode() != targetFingerprint {
		extra[codexFingerprintModeExtraKey] = string(targetFingerprint)
		// seed 由 prepareCodexFingerprintExtraForUpdate 在 Update 内自动签发。
	}
	if (isOpenAIOAuthLike(account) || isAnthropicOAuthLike(account)) && targetTLS == "standard" && (account.IsTLSFingerprintEnabled() || account.Extra["tls_fingerprint_builtin"] != nil || account.Extra["tls_fingerprint_profile_id"] != nil) {
		extra["enable_tls_fingerprint"] = false
		delete(extra, "tls_fingerprint_builtin")
		delete(extra, "tls_fingerprint_profile_id")
	} else if (isOpenAIOAuthLike(account) || isAnthropicOAuthLike(account)) && targetTLS != "standard" && (!account.IsTLSFingerprintEnabled() || account.Extra["tls_fingerprint_builtin"] != targetTLS) {
		extra["enable_tls_fingerprint"] = true
		extra["tls_fingerprint_builtin"] = targetTLS
		delete(extra, "tls_fingerprint_profile_id")
	}
	if account.Concurrency <= 0 {
		cap := targetConcurrency
		input.Concurrency = &cap
	}
	if account.Concurrency > 0 {
		targetConcurrency = account.Concurrency
	}
	extra[AntiDegradeMarkerExtraKey] = map[string]any{
		"enabled": true,
		"mode":    string(mode),
		// Keep an explicit runtime cap in the marker for the legacy policy too;
		// BoundAccountProtectionConcurrency uses this value on every request.
		"max_concurrency": targetConcurrency,
		"applied_at":      time.Now().UTC().Format(time.RFC3339),
		"prev":            prev,
	}
	extra[AntiDegradationExtraKey] = true
	extra[ProtectionScopeExtraKey] = "legacy"
	return s.admin.UpdateAccount(context.WithValue(ctx, mode1ManagedWriteKey{}, true), id, input)
}

// RevertAntiDegrade 一键还原：恢复快照旧值并清除标记。
func (s *AntiDegradeService) RevertAntiDegrade(ctx context.Context, id int64) (*Account, error) {
	return s.transition(ctx, id, func(planner *AntiDegradeService) error {
		_, err := planner.revertAntiDegrade(ctx, id)
		return err
	})
}

func (s *AntiDegradeService) revertAntiDegrade(ctx context.Context, id int64) (*Account, error) {
	if s == nil || s.admin == nil {
		return nil, errors.New("anti-degrade service unavailable")
	}
	account, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if !account.AntiDegradationEnabled() {
		return account, nil
	}
	prev := antiDegradePrev(account)
	if isMode1ProtectionRequested(account) && prev == nil {
		return nil, errors.New("mode1 original configuration snapshot is missing")
	}
	// 先校验快照类型：未知类型直接报错，不写库（避免半还原后快照丢失无法重试）。
	var concurrency *int
	if conc, ok := prev["concurrency"]; ok {
		switch v := conc.(type) {
		case float64:
			c := int(v)
			if v != float64(c) {
				return nil, errors.New("anti-degrade snapshot has invalid concurrency value")
			}
			concurrency = &c
		case int:
			concurrency = &v
		case int64:
			c := int(v)
			concurrency = &c
		default:
			return nil, errors.New("anti-degrade snapshot has unsupported concurrency type")
		}
	}
	extra := maps.Clone(account.Extra)
	if extra == nil {
		extra = make(map[string]any)
	}
	// Concurrency is now independent from identity/TLS. Restoring a strategy
	// must not undo the administrator's later concurrency edits.
	_ = concurrency // historical snapshot remains validated but is not reapplied
	input := &UpdateAccountInput{Extra: extra}
	extra[AntiDegradationExtraKey] = false
	extra[ProtectionScopeExtraKey] = "disabled"
	if isMode1ProtectionRequested(account) {
		for _, key := range mode1ManagedExtraKeys {
			if key == AntiDegradeMarkerExtraKey {
				continue
			}
			if value, exists := prev[key]; exists && value != nil {
				extra[key] = value
			} else {
				delete(extra, key)
			}
		}
		delete(extra, AntiDegradeMarkerExtraKey)
		return s.admin.UpdateAccount(context.WithValue(ctx, mode1ManagedWriteKey{}, true), id, input)
	}
	// New snapshots include all managed keys, with nil recording absence.
	// Historical snapshots were sparse: omitted keys must be left intact.
	for _, key := range mode1ManagedExtraKeys {
		if key == AntiDegradeMarkerExtraKey {
			continue
		}
		if value, exists := prev[key]; exists {
			if value == nil {
				delete(extra, key)
			} else {
				extra[key] = value
			}
		}
	}
	delete(extra, AntiDegradeMarkerExtraKey)
	return s.admin.UpdateAccount(context.WithValue(ctx, mode1ManagedWriteKey{}, true), id, input)
}

func snapshotAntiDegradeConfig(account *Account) map[string]any {
	prev := map[string]any{"concurrency": account.Concurrency}
	for _, key := range mode1ManagedExtraKeys {
		if key != AntiDegradeMarkerExtraKey {
			prev[key] = account.Extra[key]
		}
	}
	return prev
}
