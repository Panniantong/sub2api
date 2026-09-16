package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SettingKeyAccountHealthSettings 全站账号健康分配置（JSON，见 AccountHealthSettings）。
const SettingKeyAccountHealthSettings = "account_health_settings"

// accountHealthReasonPrefix 健康服务写入的临时隔离原因前缀。恢复时只清理
// 带此前缀的行，避免误解除其他功能（限流/刷新）设置的隔离。
const accountHealthReasonPrefix = "health:"

// AccountHealthSettings 账号健康分阈值配置。
type AccountHealthSettings struct {
	Enabled         bool    `json:"enabled"`
	WindowMinutes   int     `json:"window_minutes"`
	MinSamples      int     `json:"min_samples"`
	IsolateErrRate  float64 `json:"isolate_err_rate"`
	RecoverErrRate  float64 `json:"recover_err_rate"`
	CooldownMinutes int     `json:"cooldown_minutes"`
	IntervalSeconds int     `json:"interval_seconds"`
}

// DefaultAccountHealthSettings 默认阈值：10 分钟窗口 ≥10 个样本且错误率 ≥50% 隔离 30 分钟。
func DefaultAccountHealthSettings() AccountHealthSettings {
	return AccountHealthSettings{
		Enabled:         true,
		WindowMinutes:   10,
		MinSamples:      10,
		IsolateErrRate:  0.5,
		RecoverErrRate:  0.2,
		CooldownMinutes: 30,
		IntervalSeconds: 60,
	}
}

// AccountHealthSnapshot 单个账号的健康快照（管理端展示）。
type AccountHealthSnapshot struct {
	AccountID     int64      `json:"account_id"`
	Name          string     `json:"name"`
	Platform      string     `json:"platform"`
	Score         int        `json:"score"`
	ErrRate       float64    `json:"err_rate"`
	AvgLatencyMs  *int       `json:"avg_latency_ms,omitempty"`
	Total         int64      `json:"total"`
	Errors        int64      `json:"errors"`
	State         string     `json:"state"` // healthy / degraded / isolated
	Isolated      bool       `json:"isolated"`
	IsolateReason string     `json:"isolate_reason,omitempty"`
	IsolatedUntil *time.Time `json:"isolated_until,omitempty"`
	EvaluatedAt   time.Time  `json:"evaluated_at"`
}

// AccountHealthAccountStore 健康评估需要的账号写接口（AccountRepository 已实现）。
type AccountHealthAccountStore interface {
	SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error
	ClearTempUnschedulable(ctx context.Context, id int64) error
}

// AccountHealthService 全站账号健康分：按窗口聚合 usage_logs（成功）与
// ops_error_logs（失败），错误率超阈自动临时隔离，恢复后自动解除。
// 隔离复用 TempUnschedulable 机制，到期自动恢复；只动 reason 前缀为 health: 的行。
type AccountHealthService struct {
	db          *sql.DB
	accountRepo AccountHealthAccountStore
	settingRepo SettingRepository

	mu       sync.RWMutex
	snapshot map[int64]AccountHealthSnapshot
	updated  time.Time

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewAccountHealthService 创建健康服务（不启动后台循环，测试友好）。
func NewAccountHealthService(db *sql.DB, accountRepo AccountHealthAccountStore, settingRepo SettingRepository) *AccountHealthService {
	return &AccountHealthService{
		db:          db,
		accountRepo: accountRepo,
		settingRepo: settingRepo,
		snapshot:    make(map[int64]AccountHealthSnapshot),
		stopCh:      make(chan struct{}),
	}
}

// ProvideAccountHealthService 创建并启动健康服务（wire 用）。
func ProvideAccountHealthService(db *sql.DB, accountRepo AccountRepository, settingRepo SettingRepository) *AccountHealthService {
	svc := NewAccountHealthService(db, accountRepo, settingRepo)
	svc.Start()
	return svc
}

// Start 启动周期评估（60s 一次，阈值可配）。
func (s *AccountHealthService) Start() {
	if s == nil || s.db == nil || s.accountRepo == nil {
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
func (s *AccountHealthService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *AccountHealthService) currentSettings() AccountHealthSettings {
	cfg := DefaultAccountHealthSettings()
	if s == nil || s.settingRepo == nil {
		return cfg
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAccountHealthSettings)
	if err != nil || strings.TrimSpace(raw) == "" {
		return cfg
	}
	var parsed AccountHealthSettings
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		slog.Warn("account_health.bad_settings", "error", err)
		return cfg
	}
	return normalizeAccountHealthSettings(parsed)
}

func normalizeAccountHealthSettings(in AccountHealthSettings) AccountHealthSettings {
	def := DefaultAccountHealthSettings()
	// Enabled 显式以存储为准（零值 false 即关闭，不回填默认 true）。
	if in.WindowMinutes <= 0 {
		in.WindowMinutes = def.WindowMinutes
	}
	if in.MinSamples <= 0 {
		in.MinSamples = def.MinSamples
	}
	if in.IsolateErrRate <= 0 || in.IsolateErrRate > 1 {
		in.IsolateErrRate = def.IsolateErrRate
	}
	if in.RecoverErrRate < 0 || in.RecoverErrRate > 1 {
		in.RecoverErrRate = def.RecoverErrRate
	}
	if in.RecoverErrRate >= in.IsolateErrRate {
		in.RecoverErrRate = def.RecoverErrRate
	}
	if in.CooldownMinutes <= 0 {
		in.CooldownMinutes = def.CooldownMinutes
	}
	if in.IntervalSeconds < 10 {
		in.IntervalSeconds = def.IntervalSeconds
	}
	return in
}

// GetSettings 读取当前配置（无存储时返回默认）。
func (s *AccountHealthService) GetSettings(ctx context.Context) AccountHealthSettings {
	if s == nil {
		return DefaultAccountHealthSettings()
	}
	return s.currentSettings()
}

// UpdateSettings 校验并保存配置。
func (s *AccountHealthService) UpdateSettings(ctx context.Context, in AccountHealthSettings) (AccountHealthSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultAccountHealthSettings(), nil
	}
	norm := normalizeAccountHealthSettings(in)
	raw, err := json.Marshal(norm)
	if err != nil {
		return norm, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAccountHealthSettings, string(raw)); err != nil {
		return norm, err
	}
	return norm, nil
}

// Snapshot 返回最近一次评估快照（管理端列表）。
func (s *AccountHealthService) Snapshot() []AccountHealthSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AccountHealthSnapshot, 0, len(s.snapshot))
	for _, v := range s.snapshot {
		out = append(out, v)
	}
	return out
}

type accountHealthRow struct {
	id       int64
	name     string
	platform string
	ok       int64
	avgMs    sql.NullFloat64
	err      int64
	until    sql.NullTime
	reason   sql.NullString
}

func (s *AccountHealthService) runOnce() {
	cfg := s.currentSettings()
	if !cfg.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := s.queryWindowStats(ctx, cfg.WindowMinutes)
	if err != nil {
		log.Printf("[AccountHealth] query window stats failed: %v", err)
		return
	}
	now := time.Now()
	next := make(map[int64]AccountHealthSnapshot, len(rows))
	for _, r := range rows {
		snap := decideAccountHealth(r, cfg, now)
		next[r.id] = snap
		if snap.State == "isolated" && !r.isHealthIsolated(now) {
			// 同列多写者（限流/刷新/传输层）：若其他子系统已设冷却且未到期，不覆盖。
			if r.until.Valid && r.until.Time.After(now) {
				next[r.id] = snap
				continue
			}
			until := now.Add(time.Duration(cfg.CooldownMinutes) * time.Minute)
			reason := accountHealthReasonPrefix + "auto err_rate=" + formatRate(snap.ErrRate)
			if err := s.accountRepo.SetTempUnschedulable(ctx, r.id, until, reason); err != nil {
				log.Printf("[AccountHealth] isolate account %d failed: %v", r.id, err)
				snap.Isolated = false
				next[r.id] = snap
				continue
			}
			slog.Info("account_health.isolated", "account_id", r.id, "err_rate", snap.ErrRate, "total", snap.Total)
			snap.Isolated = true
			snap.IsolateReason = reason
			snap.IsolatedUntil = &until
			next[r.id] = snap
		}
		if snap.State != "isolated" && r.isHealthIsolated(now) && r.hasEnoughSamples(cfg) && snap.ErrRate <= cfg.RecoverErrRate {
			if err := s.accountRepo.ClearTempUnschedulable(ctx, r.id); err != nil {
				log.Printf("[AccountHealth] recover account %d failed: %v", r.id, err)
				continue
			}
			slog.Info("account_health.recovered", "account_id", r.id, "err_rate", snap.ErrRate)
		}
	}
	s.mu.Lock()
	s.snapshot = next
	s.updated = now
	s.mu.Unlock()
}

func (r accountHealthRow) isHealthIsolated(now time.Time) bool {
	return r.until.Valid && r.until.Time.After(now) &&
		r.reason.Valid && strings.HasPrefix(r.reason.String, accountHealthReasonPrefix)
}

func (r accountHealthRow) hasEnoughSamples(cfg AccountHealthSettings) bool {
	return r.ok+r.err >= int64(cfg.MinSamples)
}

// decideAccountHealth 纯函数：由窗口统计计算评分与期望状态（可单测）。
func decideAccountHealth(r accountHealthRow, cfg AccountHealthSettings, now time.Time) AccountHealthSnapshot {
	snap := AccountHealthSnapshot{
		AccountID: r.id, Name: r.name, Platform: r.platform,
		EvaluatedAt: now,
	}
	total := r.ok + r.err
	snap.Total = total
	snap.Errors = r.err
	if total == 0 {
		snap.Score = 100
		snap.State = "healthy"
	} else {
		snap.ErrRate = float64(r.err) / float64(total)
		snap.Score = int(math.Round(100 * (1 - snap.ErrRate)))
		snap.State = "healthy"
		if total >= int64(cfg.MinSamples) {
			if snap.ErrRate >= cfg.IsolateErrRate {
				snap.State = "isolated"
			} else if snap.ErrRate >= cfg.RecoverErrRate {
				snap.State = "degraded"
			}
		}
	}
	if r.avgMs.Valid {
		v := int(math.Round(r.avgMs.Float64))
		snap.AvgLatencyMs = &v
	}
	if r.until.Valid {
		u := r.until.Time
		snap.IsolatedUntil = &u
	}
	if r.reason.Valid {
		snap.IsolateReason = r.reason.String
	}
	snap.Isolated = r.isHealthIsolated(now) || snap.State == "isolated"
	return snap
}

func formatRate(v float64) string {
	return strconv.FormatFloat(math.Round(v*1000)/10, 'f', 1, 64) + "%"
}

func (s *AccountHealthService) queryWindowStats(ctx context.Context, windowMinutes int) ([]accountHealthRow, error) {
	q := `
SELECT a.id, COALESCE(a.name, ''), COALESCE(a.platform, ''),
  COALESCE(u.ok_count, 0), u.avg_ms,
  COALESCE(e.err_count, 0),
  a.temp_unschedulable_until, a.temp_unschedulable_reason
FROM accounts a
LEFT JOIN (
  SELECT account_id, COUNT(*) AS ok_count, AVG(duration_ms) AS avg_ms
  FROM usage_logs
  WHERE created_at >= NOW() - ($1 || ' minutes')::interval
    AND account_id IS NOT NULL AND account_id > 0
  GROUP BY account_id
) u ON u.account_id = a.id
LEFT JOIN (
  SELECT account_id, COUNT(*) AS err_count
  FROM ops_error_logs
  WHERE created_at >= NOW() - ($1 || ' minutes')::interval
    AND COALESCE(status_code, 0) >= 400
    AND NOT COALESCE(is_business_limited, FALSE)
    AND account_id IS NOT NULL
  GROUP BY account_id
) e ON e.account_id = a.id
WHERE a.deleted_at IS NULL
  AND (u.account_id IS NOT NULL OR e.account_id IS NOT NULL
    OR COALESCE(a.temp_unschedulable_reason, '') LIKE 'health:%')`
	rows, err := s.db.QueryContext(ctx, q, windowMinutes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []accountHealthRow
	for rows.Next() {
		var r accountHealthRow
		if err := rows.Scan(&r.id, &r.name, &r.platform, &r.ok, &r.avgMs, &r.err, &r.until, &r.reason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Isolate 手动隔离账号（24h，可手动恢复）。
func (s *AccountHealthService) Isolate(ctx context.Context, id int64) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	until := time.Now().Add(24 * time.Hour)
	return s.accountRepo.SetTempUnschedulable(ctx, id, until, accountHealthReasonPrefix+"manual")
}

// Resume 手动恢复：清隔离并重置快照条目。
func (s *AccountHealthService) Resume(ctx context.Context, id int64) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	if err := s.accountRepo.ClearTempUnschedulable(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.snapshot, id)
	s.mu.Unlock()
	return nil
}
