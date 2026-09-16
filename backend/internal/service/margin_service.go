package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// SettingKeyMarginFuseSettings 毛利熔断配置（JSON，见 MarginFuseSettings）。
const SettingKeyMarginFuseSettings = "margin_fuse_settings"

// SettingKeyMarginFusedChannels 熔断中渠道（JSON map[渠道ID]熔断时间，重启不丢）。
const SettingKeyMarginFusedChannels = "margin_fused_channels"

// MarginFuseSettings 毛利熔断阈值：窗口内某渠道预估支出达标且毛利率
// 低于阈值时自动停用该渠道；冷却后自动恢复试探。
type MarginFuseSettings struct {
	Enabled         bool    `json:"enabled"`
	WindowHours     int     `json:"window_hours"`
	MinSpend        float64 `json:"min_spend"`
	MinMarginRate   float64 `json:"min_margin_rate"`
	CooldownHours   int     `json:"cooldown_hours"`
	IntervalMinutes int     `json:"interval_minutes"`
}

// DefaultMarginFuseSettings 默认：24h 窗口，预估支出 ≥$5 且毛利率 <0% 熔断，冷却 6h。
func DefaultMarginFuseSettings() MarginFuseSettings {
	return MarginFuseSettings{
		Enabled: true, WindowHours: 24, MinSpend: 5,
		MinMarginRate: 0, CooldownHours: 6, IntervalMinutes: 5,
	}
}

// MarginRow 单（渠道，分组，模型）毛利行。EstCost 为空表示无目录价、无法估算。
type MarginRow struct {
	ChannelID   *int64   `json:"channel_id"`
	ChannelName string   `json:"channel_name"`
	GroupID     *int64   `json:"group_id"`
	GroupName   string   `json:"group_name"`
	Model       string   `json:"model"`
	Requests    int64    `json:"requests"`
	Revenue     float64  `json:"revenue"`
	EstCost     *float64 `json:"est_cost,omitempty"`
	Margin      *float64 `json:"margin,omitempty"`
	MarginRate  *float64 `json:"margin_rate,omitempty"`
}

// MarginFuseEvent 熔断/恢复事件（内存保留近 100 条）。
type MarginFuseEvent struct {
	At        time.Time `json:"at"`
	ChannelID int64     `json:"channel_id"`
	Name      string    `json:"name"`
	Action    string    `json:"action"` // fused / recovered
	Reason    string    `json:"reason"`
}

// ChannelStatusStore 熔断需要的渠道读写接口（ChannelService 已实现）。
type ChannelStatusStore interface {
	GetByID(ctx context.Context, id int64) (*Channel, error)
	Update(ctx context.Context, id int64, input *UpdateChannelInput) (*Channel, error)
}

// MarginService 毛利看板 + 熔断：收入取 usage_logs.actual_cost（用户实扣），
// 成本按目录价 × token 估算（中转上游真实成本不可见时行业通用做法）。
type MarginService struct {
	db             *sql.DB
	channels       ChannelStatusStore
	billingService *BillingService
	settingRepo    SettingRepository

	mu     sync.RWMutex
	fused  map[int64]time.Time // 熔断中渠道 → 熔断时间
	events []MarginFuseEvent

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewMarginService 创建毛利服务（不启动后台循环）。
func NewMarginService(db *sql.DB, channels ChannelStatusStore, billing *BillingService, settingRepo SettingRepository) *MarginService {
	return &MarginService{
		db: db, channels: channels, billingService: billing,
		settingRepo: settingRepo, fused: make(map[int64]time.Time),
		stopCh: make(chan struct{}),
	}
}

// ProvideMarginService 创建并启动毛利服务（wire 用）。
func ProvideMarginService(db *sql.DB, channelService *ChannelService, billing *BillingService, settingRepo SettingRepository) *MarginService {
	svc := NewMarginService(db, channelService, billing, settingRepo)
	svc.Start()
	return svc
}

// Start 启动周期熔断评估。
func (s *MarginService) Start() {
	if s == nil || s.db == nil || s.channels == nil {
		return
	}
	s.loadFused()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Duration(s.currentSettings().IntervalMinutes) * time.Minute)
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
func (s *MarginService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *MarginService) currentSettings() MarginFuseSettings {
	cfg := DefaultMarginFuseSettings()
	if s == nil || s.settingRepo == nil {
		return cfg
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyMarginFuseSettings)
	if err != nil || strings.TrimSpace(raw) == "" {
		return cfg
	}
	var parsed MarginFuseSettings
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		slog.Warn("margin.bad_settings", "error", err)
		return cfg
	}
	return normalizeMarginFuseSettings(parsed)
}

func normalizeMarginFuseSettings(in MarginFuseSettings) MarginFuseSettings {
	def := DefaultMarginFuseSettings()
	if in.WindowHours <= 0 {
		in.WindowHours = def.WindowHours
	}
	if in.MinSpend < 0 {
		in.MinSpend = def.MinSpend
	}
	if in.CooldownHours <= 0 {
		in.CooldownHours = def.CooldownHours
	}
	if in.IntervalMinutes < 1 {
		in.IntervalMinutes = def.IntervalMinutes
	}
	return in
}

// GetSettings 读取当前配置。
func (s *MarginService) GetSettings(_ context.Context) MarginFuseSettings {
	if s == nil {
		return DefaultMarginFuseSettings()
	}
	return s.currentSettings()
}

// UpdateSettings 保存配置。
func (s *MarginService) UpdateSettings(ctx context.Context, in MarginFuseSettings) (MarginFuseSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultMarginFuseSettings(), nil
	}
	norm := normalizeMarginFuseSettings(in)
	raw, err := json.Marshal(norm)
	if err != nil {
		return norm, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyMarginFuseSettings, string(raw)); err != nil {
		return norm, err
	}
	return norm, nil
}

// Events 返回近期熔断/恢复事件。
func (s *MarginService) Events() []MarginFuseEvent {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MarginFuseEvent, len(s.events))
	copy(out, s.events)
	return out
}

type marginAggRow struct {
	channelID   sql.NullInt64
	channelName sql.NullString
	groupID     sql.NullInt64
	groupName   sql.NullString
	model       string
	requests    int64
	revenue     float64
	inTok       int64
	outTok      int64
	crTok       int64
	cwTok       int64
}

// Summary 聚合毛利：groupID 为 nil 表示全部分组。
func (s *MarginService) Summary(ctx context.Context, hours int, groupID *int64) ([]MarginRow, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if hours <= 0 || hours > 24*90 {
		hours = 24
	}
	q := `
SELECT u.channel_id, c.name, u.group_id, g.name, u.model,
  COUNT(*), COALESCE(SUM(u.actual_cost),0),
  COALESCE(SUM(u.input_tokens),0), COALESCE(SUM(u.output_tokens),0),
  COALESCE(SUM(u.cache_read_tokens),0), COALESCE(SUM(u.cache_creation_tokens),0)
FROM usage_logs u
LEFT JOIN channels c ON c.id = u.channel_id
LEFT JOIN groups g ON g.id = u.group_id
WHERE u.created_at >= NOW() - ($1 || ' hours')::interval
  AND ($2::bigint IS NULL OR u.group_id = $2)
GROUP BY u.channel_id, c.name, u.group_id, g.name, u.model
ORDER BY SUM(u.actual_cost) DESC
LIMIT 2000`
	var gid any
	if groupID != nil {
		gid = *groupID
	}
	rows, err := s.db.QueryContext(ctx, q, hours, gid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]MarginRow, 0)
	for rows.Next() {
		var r marginAggRow
		if err := rows.Scan(&r.channelID, &r.channelName, &r.groupID, &r.groupName,
			&r.model, &r.requests, &r.revenue, &r.inTok, &r.outTok, &r.crTok, &r.cwTok); err != nil {
			return nil, err
		}
		out = append(out, s.toMarginRow(r))
	}
	return out, rows.Err()
}

func (s *MarginService) toMarginRow(r marginAggRow) MarginRow {
	row := MarginRow{
		Model: r.model, Requests: r.requests, Revenue: r.revenue,
		ChannelName: "direct",
		GroupName:   "ungrouped",
	}
	if r.channelID.Valid {
		id := r.channelID.Int64
		row.ChannelID = &id
	}
	if r.channelName.Valid && r.channelName.String != "" {
		row.ChannelName = r.channelName.String
	}
	if r.groupID.Valid {
		id := r.groupID.Int64
		row.GroupID = &id
	}
	if r.groupName.Valid && r.groupName.String != "" {
		row.GroupName = r.groupName.String
	}
	if s == nil || s.billingService == nil {
		return row
	}
	unit, err := s.billingService.GetModelPricing(r.model)
	if err != nil || unit == nil {
		return row
	}
	est := float64(r.inTok)*unit.InputPricePerToken +
		float64(r.outTok)*unit.OutputPricePerToken +
		float64(r.crTok)*unit.CacheReadPricePerToken +
		float64(r.cwTok)*unit.CacheCreationPricePerToken
	row.EstCost = &est
	margin := r.revenue - est
	row.Margin = &margin
	if r.revenue > 0 {
		rate := margin / r.revenue
		row.MarginRate = &rate
	}
	return row
}

// loadFused 从配置恢复熔断跟踪（重启后已 disabled 的渠道继续被跟踪）。
func (s *MarginService) loadFused() {
	if s == nil || s.settingRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyMarginFusedChannels)
	if err != nil || strings.TrimSpace(raw) == "" {
		return
	}
	var m map[int64]time.Time
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		slog.Warn("margin.bad_fused", "error", err)
		return
	}
	s.mu.Lock()
	for id, at := range m {
		if id > 0 {
			s.fused[id] = at
		}
	}
	s.mu.Unlock()
}

func (s *MarginService) persistFused() {
	if s == nil || s.settingRepo == nil {
		return
	}
	s.mu.RLock()
	m := make(map[int64]time.Time, len(s.fused))
	for id, at := range s.fused {
		m[id] = at
	}
	s.mu.RUnlock()
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.settingRepo.Set(ctx, SettingKeyMarginFusedChannels, string(raw)); err != nil {
		slog.Warn("margin.persist_fused_failed", "error", err)
	}
}

func (s *MarginService) pushEvent(ev MarginFuseEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append([]MarginFuseEvent{ev}, s.events...)
	if len(s.events) > 100 {
		s.events = s.events[:100]
	}
}

func (s *MarginService) runOnce() {
	cfg := s.currentSettings()
	if !cfg.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rows, err := s.Summary(ctx, cfg.WindowHours, nil)
	if err != nil {
		log.Printf("[Margin] summary failed: %v", err)
		return
	}
	// 按渠道汇总
	type agg struct {
		name    string
		revenue float64
		cost    float64
		known   bool
	}
	byChannel := make(map[int64]*agg)
	for _, r := range rows {
		if r.ChannelID == nil || r.EstCost == nil {
			continue
		}
		a := byChannel[*r.ChannelID]
		if a == nil {
			a = &agg{name: r.ChannelName}
			byChannel[*r.ChannelID] = a
		}
		a.revenue += r.Revenue
		a.cost += *r.EstCost
		a.known = true
	}
	now := time.Now()
	for id, a := range byChannel {
		if !a.known || a.cost < cfg.MinSpend {
			continue
		}
		rate := 0.0
		if a.revenue > 0 {
			rate = (a.revenue - a.cost) / a.revenue
		} else if a.cost > 0 {
			rate = -1
		}
		s.mu.RLock()
		_, fused := s.fused[id]
		s.mu.RUnlock()
		if fused {
			continue
		}
		if rate >= cfg.MinMarginRate {
			continue
		}
		// 熔断语义：停用渠道即摘掉它的自定义定价/映射/限制，
		// 分组回落目录价。若亏损本就来自渠道低价，此举直接止血；
		// 若亏损来自的分级/全站覆盖，面板仍可见，由管理员人工处理。
		ch, err := s.channels.GetByID(ctx, id)
		if err != nil || ch == nil || ch.Status != StatusActive {
			continue
		}
		if _, err := s.channels.Update(ctx, id, &UpdateChannelInput{Status: StatusDisabled}); err != nil {
			log.Printf("[Margin] fuse channel %d failed: %v", id, err)
			continue
		}
		s.mu.Lock()
		s.fused[id] = now
		s.mu.Unlock()
		s.persistFused()
		slog.Warn("margin.channel_fused", "channel_id", id, "name", a.name, "margin_rate", rate)
		s.pushEvent(MarginFuseEvent{At: now, ChannelID: id, Name: a.name,
			Action: "fused", Reason: "margin_rate below threshold"})
	}
	// 恢复扫描基于 DB 状态而非当窗口流量：停量渠道不在 byChannel 中，
	// 也必须能走出冷却。
	s.mu.RLock()
	fusedIDs := make([]int64, 0, len(s.fused))
	fusedAt := make(map[int64]time.Time, len(s.fused))
	for id, at := range s.fused {
		fusedIDs = append(fusedIDs, id)
		fusedAt[id] = at
	}
	s.mu.RUnlock()
	for _, id := range fusedIDs {
		if now.Sub(fusedAt[id]) < time.Duration(cfg.CooldownHours)*time.Hour {
			continue
		}
		ch, err := s.channels.GetByID(ctx, id)
		if err != nil || ch == nil {
			continue
		}
		if ch.Status != StatusDisabled {
			// 管理员已手动恢复：不再跟踪
			s.mu.Lock()
			delete(s.fused, id)
			s.mu.Unlock()
			s.persistFused()
			continue
		}
		if _, err := s.channels.Update(ctx, id, &UpdateChannelInput{Status: StatusActive}); err != nil {
			log.Printf("[Margin] recover channel %d failed: %v", id, err)
			continue
		}
		s.mu.Lock()
		delete(s.fused, id)
		s.mu.Unlock()
		s.persistFused()
		slog.Info("margin.channel_recovered", "channel_id", id)
		s.pushEvent(MarginFuseEvent{At: now, ChannelID: id,
			Action: "recovered", Reason: "cooldown elapsed, probing"})
	}
}

// Unfuse 手动恢复渠道并清除熔断跟踪。
func (s *MarginService) Unfuse(ctx context.Context, id int64) error {
	if s == nil || s.channels == nil {
		return nil
	}
	if _, err := s.channels.Update(ctx, id, &UpdateChannelInput{Status: StatusActive}); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.fused, id)
	s.mu.Unlock()
	s.pushEvent(MarginFuseEvent{At: time.Now(), ChannelID: id, Action: "recovered", Reason: "manual"})
	return nil
}
