package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// SettingKeySpendGuardSettings 烧钱防护配置（JSON，见 SpendGuardSettings）。
const SettingKeySpendGuardSettings = "spend_guard_settings"

// SpendGuardSettings 烧钱防护阈值：窗口内某 key 的 token 速率或错误率
// 超阈即冻结该 key（status=disabled），仅管理员手动解冻。
type SpendGuardSettings struct {
	Enabled         bool    `json:"enabled"`
	WindowMinutes   int     `json:"window_minutes"`
	TokensPerMinute float64 `json:"tokens_per_minute"`
	MinRequests     int     `json:"min_requests"`
	MaxErrorRate    float64 `json:"max_error_rate"`
	IntervalSeconds int     `json:"interval_seconds"`
}

// DefaultSpendGuardSettings 默认：5 分钟窗口，速率 ≥200 万 token/分钟或
// 错误率 ≥80%（≥5 请求）冻结。默认关闭，由管理员显式启用。
func DefaultSpendGuardSettings() SpendGuardSettings {
	return SpendGuardSettings{
		Enabled: false, WindowMinutes: 5, TokensPerMinute: 2000000,
		MinRequests: 5, MaxErrorRate: 0.8, IntervalSeconds: 60,
	}
}

// SpendGuardOffender 烧钱嫌疑 key 行（管理端展示）。
type SpendGuardOffender struct {
	APIKeyID     int64   `json:"api_key_id"`
	Name         string  `json:"name"`
	UserID       int64   `json:"user_id"`
	Status       string  `json:"status"`
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	TokensPerMin float64 `json:"tokens_per_min"`
	Errors       int64   `json:"errors"`
	ErrRate      float64 `json:"err_rate"`
	Frozen       bool    `json:"frozen"`
}

// SpendGuardEvent 冻结/解冻事件（内存保留近 100 条）。
type SpendGuardEvent struct {
	At       time.Time `json:"at"`
	APIKeyID int64     `json:"api_key_id"`
	Name     string    `json:"name"`
	Action   string    `json:"action"` // frozen / unfrozen
	Reason   string    `json:"reason"`
}

// SpendGuardKeyStore 冻结需要的 key 读写接口（APIKeyService 已实现）。
type SpendGuardKeyStore interface {
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	Update(ctx context.Context, id int64, userID int64, req UpdateAPIKeyRequest) (*APIKey, error)
}

// SpendGuardService 烧钱防护：窗口 token 速率 / 错误率超阈自动冻结 key。
type SpendGuardService struct {
	db       *sql.DB
	keys     SpendGuardKeyStore
	settings SettingRepository

	mu     sync.RWMutex
	frozen map[int64]time.Time
	events []SpendGuardEvent

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewSpendGuardService 创建烧钱防护服务（不启动后台循环）。
func NewSpendGuardService(db *sql.DB, keys SpendGuardKeyStore, settingRepo SettingRepository) *SpendGuardService {
	return &SpendGuardService{
		db: db, keys: keys, settings: settingRepo,
		frozen: make(map[int64]time.Time), stopCh: make(chan struct{}),
	}
}

// ProvideSpendGuardService 创建并启动烧钱防护服务（wire 用）。
func ProvideSpendGuardService(db *sql.DB, apiKeyService *APIKeyService, settingRepo SettingRepository) *SpendGuardService {
	svc := NewSpendGuardService(db, apiKeyService, settingRepo)
	svc.Start()
	return svc
}

// Start 启动周期评估。
func (s *SpendGuardService) Start() {
	if s == nil || s.db == nil || s.keys == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Duration(s.currentSettings().IntervalSeconds) * time.Second)
		defer ticker.Stop()
		s.runOnce()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止后台循环。
func (s *SpendGuardService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *SpendGuardService) currentSettings() SpendGuardSettings {
	cfg := DefaultSpendGuardSettings()
	if s == nil || s.settings == nil {
		return cfg
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.settings.GetValue(ctx, SettingKeySpendGuardSettings)
	if err != nil || strings.TrimSpace(raw) == "" {
		return cfg
	}
	var parsed SpendGuardSettings
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		slog.Warn("spend_guard.bad_settings", "error", err)
		return cfg
	}
	return normalizeSpendGuardSettings(parsed)
}

func normalizeSpendGuardSettings(in SpendGuardSettings) SpendGuardSettings {
	def := DefaultSpendGuardSettings()
	if in.WindowMinutes <= 0 {
		in.WindowMinutes = def.WindowMinutes
	}
	if in.TokensPerMinute < 0 {
		in.TokensPerMinute = def.TokensPerMinute
	}
	if in.MinRequests <= 0 {
		in.MinRequests = def.MinRequests
	}
	if in.MaxErrorRate <= 0 || in.MaxErrorRate > 1 {
		in.MaxErrorRate = def.MaxErrorRate
	}
	if in.IntervalSeconds < 10 {
		in.IntervalSeconds = def.IntervalSeconds
	}
	return in
}

// GetSettings 读取当前配置。
func (s *SpendGuardService) GetSettings(_ context.Context) SpendGuardSettings {
	if s == nil {
		return DefaultSpendGuardSettings()
	}
	return s.currentSettings()
}

// UpdateSettings 保存配置。
func (s *SpendGuardService) UpdateSettings(ctx context.Context, in SpendGuardSettings) (SpendGuardSettings, error) {
	if s == nil || s.settings == nil {
		return DefaultSpendGuardSettings(), nil
	}
	norm := normalizeSpendGuardSettings(in)
	raw, err := json.Marshal(norm)
	if err != nil {
		return norm, err
	}
	if err := s.settings.Set(ctx, SettingKeySpendGuardSettings, string(raw)); err != nil {
		return norm, err
	}
	return norm, nil
}

// Events 返回近期冻结/解冻事件。
func (s *SpendGuardService) Events() []SpendGuardEvent {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SpendGuardEvent, len(s.events))
	copy(out, s.events)
	return out
}

func (s *SpendGuardService) pushEvent(ev SpendGuardEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append([]SpendGuardEvent{ev}, s.events...)
	if len(s.events) > 100 {
		s.events = s.events[:100]
	}
}

type spendGuardRow struct {
	keyID  int64
	name   string
	userID int64
	status string
	reqs   int64
	tokens int64
	errs   int64
}

// Offenders 查询窗口内各 key 的用量与错误（管理端列表与评估共用）。
func (s *SpendGuardService) Offenders(ctx context.Context, windowMinutes int) ([]SpendGuardOffender, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	// usage 与 errors 双源 UNION：纯报错（零成功行）的 key 也必须可见，
	// 否则错误率冻结对最坏情况失效。
	q := `
SELECT ak.id, COALESCE(ak.name, ''), COALESCE(ak.user_id, 0), COALESCE(ak.status, ''),
  COALESCE(SUM(t.reqs), 0), COALESCE(SUM(t.toks), 0), COALESCE(SUM(t.errs), 0)
FROM (
  SELECT api_key_id AS key_id, COUNT(*) AS reqs,
    SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens) AS toks,
    0 AS errs
  FROM usage_logs
  WHERE created_at >= NOW() - ($1 || ' minutes')::interval
    AND api_key_id IS NOT NULL AND api_key_id > 0
  GROUP BY api_key_id
  UNION ALL
  SELECT api_key_id AS key_id, 0 AS reqs, 0 AS toks, COUNT(*) AS errs
  FROM ops_error_logs
  WHERE created_at >= NOW() - ($1 || ' minutes')::interval
    AND COALESCE(status_code, 0) >= 400
    AND NOT COALESCE(is_business_limited, FALSE)
    AND api_key_id IS NOT NULL
  GROUP BY api_key_id
) t
JOIN api_keys ak ON ak.id = t.key_id
GROUP BY ak.id, ak.name, ak.user_id, ak.status
ORDER BY SUM(t.toks) DESC, SUM(t.errs) DESC
LIMIT 500`
	rows, err := s.db.QueryContext(ctx, q, windowMinutes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SpendGuardOffender
	for rows.Next() {
		var r spendGuardRow
		if err := rows.Scan(&r.keyID, &r.name, &r.userID, &r.status, &r.reqs, &r.tokens, &r.errs); err != nil {
			return nil, err
		}
		o := SpendGuardOffender{
			APIKeyID: r.keyID, Name: r.name, UserID: r.userID, Status: r.status,
			Requests: r.reqs, Tokens: r.tokens, Errors: r.errs,
			TokensPerMin: float64(r.tokens) / float64(windowMinutes),
		}
		if total := r.reqs + r.errs; total > 0 {
			o.ErrRate = float64(r.errs) / float64(total)
		}
		s.mu.RLock()
		_, o.Frozen = s.frozen[r.keyID]
		s.mu.RUnlock()
		out = append(out, o)
	}
	return out, rows.Err()
}

func spendGuardBreach(o SpendGuardOffender, cfg SpendGuardSettings) (bool, string) {
	if o.Requests+o.Errors < int64(cfg.MinRequests) {
		return false, ""
	}
	if cfg.TokensPerMinute > 0 && o.TokensPerMin >= cfg.TokensPerMinute {
		return true, "token velocity"
	}
	if o.Errors > 0 && o.ErrRate >= cfg.MaxErrorRate {
		return true, "error rate"
	}
	return false, ""
}

func (s *SpendGuardService) runOnce() {
	cfg := s.currentSettings()
	if !cfg.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	offenders, err := s.Offenders(ctx, cfg.WindowMinutes)
	if err != nil {
		log.Printf("[SpendGuard] offenders query failed: %v", err)
		return
	}
	now := time.Now()
	for _, o := range offenders {
		ok, reason := spendGuardBreach(o, cfg)
		if !ok {
			continue
		}
		s.mu.RLock()
		_, already := s.frozen[o.APIKeyID]
		s.mu.RUnlock()
		if already || o.Status != StatusAPIKeyActive {
			continue
		}
		key, err := s.keys.GetByID(ctx, o.APIKeyID)
		if err != nil || key == nil || key.Status != StatusAPIKeyActive {
			continue
		}
		disabled := StatusAPIKeyDisabled
		if _, err := s.keys.Update(ctx, o.APIKeyID, key.UserID, UpdateAPIKeyRequest{Status: &disabled}); err != nil {
			log.Printf("[SpendGuard] freeze key %d failed: %v", o.APIKeyID, err)
			continue
		}
		s.mu.Lock()
		s.frozen[o.APIKeyID] = now
		s.mu.Unlock()
		slog.Warn("spend_guard.key_frozen", "api_key_id", o.APIKeyID, "reason", reason,
			"tokens_per_min", o.TokensPerMin, "err_rate", o.ErrRate)
		s.pushEvent(SpendGuardEvent{At: now, APIKeyID: o.APIKeyID, Name: o.Name,
			Action: "frozen", Reason: reason})
	}
}

// Unfreeze 手动解冻 key（恢复 active 并清除跟踪）。
// 仅当 key 当前为 disabled 才写回，避免覆盖 quota_exhausted/expired 等中间态。
func (s *SpendGuardService) Unfreeze(ctx context.Context, id int64) error {
	if s == nil || s.keys == nil {
		return nil
	}
	key, err := s.keys.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if key == nil {
		return errors.New("api key not found")
	}
	if key.Status != StatusAPIKeyDisabled {
		s.mu.Lock()
		delete(s.frozen, id)
		s.mu.Unlock()
		return nil
	}
	active := StatusAPIKeyActive
	if _, err := s.keys.Update(ctx, id, key.UserID, UpdateAPIKeyRequest{Status: &active}); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.frozen, id)
	s.mu.Unlock()
	s.pushEvent(SpendGuardEvent{At: time.Now(), APIKeyID: id, Name: key.Name,
		Action: "unfrozen", Reason: "manual"})
	return nil
}
