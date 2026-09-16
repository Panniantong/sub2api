package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// SettingKeyTieredRoutingSettings 分级路由规则（JSON，见 TieredRoutingSettings）。
const SettingKeyTieredRoutingSettings = "tiered_routing_settings"

// 账号池与用户等级：premium 用户优先 premium 池账号，无可用时回退全池。
const (
	TierStandard = "standard"
	TierPremium  = "premium"
)

// AccountPoolExtraKey 账号 extra 中的账号池标记键。
const AccountPoolExtraKey = "pool"

// TieredRoutingSettings 分级路由规则。
type TieredRoutingSettings struct {
	Enabled              bool    `json:"enabled"`
	PremiumUserIDs       []int64 `json:"premium_user_ids"`
	MinBalanceForPremium float64 `json:"min_balance_for_premium"`
}

// DefaultTieredRoutingSettings 默认关闭，需管理员显式启用并配置规则。
func DefaultTieredRoutingSettings() TieredRoutingSettings {
	return TieredRoutingSettings{Enabled: false}
}

// TieredRoutingService 分级路由服务：规则快照缓存 + 用户等级判定。
type TieredRoutingService struct {
	settingRepo SettingRepository

	mu       sync.RWMutex
	snapshot *TieredRoutingSettings
	loadedAt time.Time
}

// tieredRoutingSnapshotTTL 规则快照 TTL。
const tieredRoutingSnapshotTTL = 60 * time.Second

// NewTieredRoutingService 创建分级路由服务。
func NewTieredRoutingService(settingRepo SettingRepository) *TieredRoutingService {
	return &TieredRoutingService{settingRepo: settingRepo}
}

func (s *TieredRoutingService) currentSettings() TieredRoutingSettings {
	def := DefaultTieredRoutingSettings()
	if s == nil || s.settingRepo == nil {
		return def
	}
	s.mu.RLock()
	if s.snapshot != nil && time.Since(s.loadedAt) < tieredRoutingSnapshotTTL {
		out := *s.snapshot
		s.mu.RUnlock()
		return out
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot != nil && time.Since(s.loadedAt) < tieredRoutingSnapshotTTL {
		return *s.snapshot
	}
	cfg := def
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if raw, err := s.settingRepo.GetValue(ctx, SettingKeyTieredRoutingSettings); err == nil && strings.TrimSpace(raw) != "" {
		var parsed TieredRoutingSettings
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			cfg = normalizeTieredRoutingSettings(parsed)
		}
	}
	s.snapshot = &cfg
	s.loadedAt = time.Now()
	return cfg
}

func normalizeTieredRoutingSettings(in TieredRoutingSettings) TieredRoutingSettings {
	ids := make([]int64, 0, len(in.PremiumUserIDs))
	seen := make(map[int64]bool, len(in.PremiumUserIDs))
	for _, id := range in.PremiumUserIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	in.PremiumUserIDs = ids
	if in.MinBalanceForPremium < 0 {
		in.MinBalanceForPremium = 0
	}
	return in
}

// Invalidate 清空规则快照（写操作后调用）。
func (s *TieredRoutingService) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.snapshot = nil
	s.loadedAt = time.Time{}
	s.mu.Unlock()
}

// GetSettings 读取当前规则。
func (s *TieredRoutingService) GetSettings(_ context.Context) TieredRoutingSettings {
	if s == nil {
		return DefaultTieredRoutingSettings()
	}
	return s.currentSettings()
}

// UpdateSettings 保存规则。
func (s *TieredRoutingService) UpdateSettings(ctx context.Context, in TieredRoutingSettings) (TieredRoutingSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultTieredRoutingSettings(), nil
	}
	norm := normalizeTieredRoutingSettings(in)
	raw, err := json.Marshal(norm)
	if err != nil {
		return norm, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyTieredRoutingSettings, string(raw)); err != nil {
		return norm, err
	}
	s.Invalidate()
	return norm, nil
}

// ResolveUserTier 判定用户等级：显式名单或余额达标即 premium，否则 standard。
// 未启用时恒返回 standard（关闭即无行为变化）。
func (s *TieredRoutingService) ResolveUserTier(user *User) string {
	if s == nil || user == nil {
		return TierStandard
	}
	cfg := s.currentSettings()
	if !cfg.Enabled {
		return TierStandard
	}
	return resolveTierByRules(user, cfg)
}

func resolveTierByRules(user *User, cfg TieredRoutingSettings) string {
	if user == nil {
		return TierStandard
	}
	for _, id := range cfg.PremiumUserIDs {
		if user.ID == id {
			return TierPremium
		}
	}
	if cfg.MinBalanceForPremium > 0 && user.Balance >= cfg.MinBalanceForPremium {
		return TierPremium
	}
	return TierStandard
}

// ResolvePool 供中间件调用：解析用户等级对应的目标账号池。
func (s *TieredRoutingService) ResolvePool(user *User) string {
	return s.ResolveUserTier(user)
}

// AccountPool 读取账号所属池：extra.pool 显式标记 premium 才进 premium 池。
func AccountPool(account *Account) string {
	if account == nil || len(account.Extra) == 0 {
		return TierStandard
	}
	if v, ok := account.Extra[AccountPoolExtraKey]; ok {
		if str, ok := v.(string); ok && strings.EqualFold(strings.TrimSpace(str), TierPremium) {
			return TierPremium
		}
	}
	return TierStandard
}

// WithTierPool 把目标账号池写入 ctx（认证中间件在鉴权成功后调用一次）。
func WithTierPool(ctx context.Context, pool string) context.Context {
	return context.WithValue(ctx, ctxkey.TierPool, pool)
}

// TierPoolFromContext 读取目标账号池，缺省 standard（不过滤）。
func TierPoolFromContext(ctx context.Context) string {
	if ctx == nil {
		return TierStandard
	}
	if pool, ok := ctx.Value(ctxkey.TierPool).(string); ok && pool == TierPremium {
		return TierPremium
	}
	return TierStandard
}

// FilterTierPoolAccounts 按目标池过滤候选账号：premium 用户优先 premium 池；
// 池内无可用时回退全量（永不 fail-closed）。
func FilterTierPoolAccounts(accounts []Account, wantPool string) []Account {
	if wantPool != TierPremium || len(accounts) == 0 {
		return accounts
	}
	matched := make([]Account, 0, len(accounts))
	for _, acc := range accounts {
		if AccountPool(&acc) == TierPremium {
			matched = append(matched, acc)
		}
	}
	if len(matched) == 0 {
		return accounts
	}
	return matched
}
