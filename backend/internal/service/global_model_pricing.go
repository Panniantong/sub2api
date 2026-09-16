package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// GlobalModelPrice 全站模型定价覆盖项：命中模型的请求在全部分组/账号
// 强制使用此处价格；未命中的模型走原定价链路（分组→渠道→LiteLLM→兜底）。
type GlobalModelPrice struct {
	ID                int64       `json:"id"`
	ModelPattern      string      `json:"model_pattern"`
	BillingMode       BillingMode `json:"billing_mode"`
	InputPrice        *float64    `json:"input_price,omitempty"`
	OutputPrice       *float64    `json:"output_price,omitempty"`
	CacheWritePrice   *float64    `json:"cache_write_price,omitempty"`
	CacheWrite1hPrice *float64    `json:"cache_write_1h_price,omitempty"`
	CacheReadPrice    *float64    `json:"cache_read_price,omitempty"`
	PerRequestPrice   *float64    `json:"per_request_price,omitempty"`
	Enabled           bool        `json:"enabled"`
	CreatedAt         time.Time   `json:"created_at,omitempty"`
	UpdatedAt         time.Time   `json:"updated_at,omitempty"`
}

// GlobalModelPriceInput 全站定价的写输入（价格均为可选，nil 表示不覆盖该项）。
type GlobalModelPriceInput struct {
	ModelPattern      string      `json:"model_pattern"`
	BillingMode       BillingMode `json:"billing_mode"`
	InputPrice        *float64    `json:"input_price"`
	OutputPrice       *float64    `json:"output_price"`
	CacheWritePrice   *float64    `json:"cache_write_price"`
	CacheWrite1hPrice *float64    `json:"cache_write_1h_price"`
	CacheReadPrice    *float64    `json:"cache_read_price"`
	PerRequestPrice   *float64    `json:"per_request_price"`
	Enabled           bool        `json:"enabled"`
}

// GlobalModelPricingRepository 全站定价存储接口（repository 包实现）。
type GlobalModelPricingRepository interface {
	ListEnabled(ctx context.Context) ([]GlobalModelPrice, error)
	ListAll(ctx context.Context) ([]GlobalModelPrice, error)
	Create(ctx context.Context, in GlobalModelPriceInput) (*GlobalModelPrice, error)
	GetByID(ctx context.Context, id int64) (*GlobalModelPrice, error)
	Update(ctx context.Context, id int64, in GlobalModelPriceInput) (*GlobalModelPrice, error)
	Delete(ctx context.Context, id int64) error
	SetEnabled(ctx context.Context, id int64, enabled bool) error
}

var ErrGlobalModelPricingNotFound = errors.New("global model pricing not found")

// ErrGlobalModelPricingDuplicate 模型已存在全站定价（model_pattern 唯一）。
var ErrGlobalModelPricingDuplicate = errors.New("global model pricing already exists for this model")

// globalModelPricingSnapshotTTL 快照 TTL：管理端改价最多延迟 60s 生效。
const globalModelPricingSnapshotTTL = 60 * time.Second

// GlobalModelPricingService 全站定价服务：内存快照 + 精确/通配匹配。
type GlobalModelPricingService struct {
	repo GlobalModelPricingRepository

	mu       sync.RWMutex
	snapshot []GlobalModelPrice
	loadedAt time.Time
}

// NewGlobalModelPricingService 创建全站定价服务（repo 可为 nil，nil 时恒返回未命中）。
func NewGlobalModelPricingService(repo GlobalModelPricingRepository) *GlobalModelPricingService {
	return &GlobalModelPricingService{repo: repo}
}

// ValidateGlobalModelPriceInput 校验写输入（单位与渠道定价一致：每 token 美元价）。
// 全部价格为空的条目无意义（会产生“覆盖却无价格”的歧义），直接拒绝；
// 非 token 模式必须给出按次价，否则计费会归零。
func ValidateGlobalModelPriceInput(in GlobalModelPriceInput) error {
	pattern := strings.ToLower(strings.TrimSpace(in.ModelPattern))
	if pattern == "" {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_REQUIRED", "model_pattern must not be empty")
	}
	if len([]rune(pattern)) > 200 {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_TOO_LONG", "model_pattern must not exceed 200 runes")
	}
	if strings.Contains(pattern, " ") {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_INVALID", "model_pattern must not contain spaces")
	}
	if stars := strings.Count(pattern, "*"); stars > 1 ||
		(stars == 1 && !strings.HasSuffix(pattern, "*")) {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_INVALID", "only a single trailing * wildcard is supported (e.g. gpt-5*)")
	}
	if core := strings.TrimSuffix(pattern, "*"); core == "" {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_INVALID", "model_pattern must contain a model name")
	}
	mode := in.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	switch mode {
	case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo:
	default:
		return infraerrors.BadRequest("GLOBAL_PRICING_MODE_INVALID", "unsupported billing_mode "+string(in.BillingMode))
	}
	for name, v := range map[string]*float64{
		"input_price": in.InputPrice, "output_price": in.OutputPrice,
		"cache_write_price": in.CacheWritePrice, "cache_write_1h_price": in.CacheWrite1hPrice,
		"cache_read_price": in.CacheReadPrice, "per_request_price": in.PerRequestPrice,
	} {
		if v != nil && *v < 0 {
			return infraerrors.BadRequest("GLOBAL_PRICING_PRICE_INVALID", name+" must not be negative")
		}
	}
	if in.InputPrice == nil && in.OutputPrice == nil && in.CacheWritePrice == nil &&
		in.CacheWrite1hPrice == nil && in.CacheReadPrice == nil && in.PerRequestPrice == nil {
		return infraerrors.BadRequest("GLOBAL_PRICING_PRICE_REQUIRED", "at least one price must be set")
	}
	if mode != BillingModeToken && in.PerRequestPrice == nil {
		return infraerrors.BadRequest("GLOBAL_PRICING_PRICE_REQUIRED", "per_request_price is required for "+string(mode)+" billing mode")
	}
	return nil
}

// snapshotLocked 读取快照（调用方需持有至少读锁判断新鲜度；过期时升级写锁重载）。
func (s *GlobalModelPricingService) snapshotPrices(ctx context.Context) []GlobalModelPrice {
	if s == nil || s.repo == nil {
		return nil
	}
	s.mu.RLock()
	if time.Since(s.loadedAt) < globalModelPricingSnapshotTTL && s.snapshot != nil {
		out := s.snapshot
		s.mu.RUnlock()
		return out
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.loadedAt) < globalModelPricingSnapshotTTL && s.snapshot != nil {
		return s.snapshot
	}
	rows, err := s.repo.ListEnabled(ctx)
	if err != nil {
		slog.Warn("global_pricing.snapshot_failed", "error", err)
		return s.snapshot
	}
	s.snapshot = rows
	s.loadedAt = time.Now()
	return rows
}

// Invalidate 清空快照，管理端写操作后调用以立即生效。
func (s *GlobalModelPricingService) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.snapshot = nil
	s.loadedAt = time.Time{}
	s.mu.Unlock()
}

// Match 命中指定模型的全站定价（精确优先，通配取最长前缀），未命中返回 nil。
// pattern 与请求名两侧都经过 normalizeChannelPricingModelName（如 claude- 系
// 的点/横线归一），避免“claude-3.5*”这类条目永不命中。
// 官方变体名（如 gpt-5.6-luna-high）会再用归一化基名重试一次，与渠道定价一致。
func (s *GlobalModelPricingService) Match(ctx context.Context, model string) *ChannelModelPricing {
	rows := s.snapshotPrices(ctx)
	if len(rows) == 0 {
		return nil
	}
	if hit := matchGlobalRows(rows, normalizeChannelPricingModelName(model)); hit != nil {
		return hit
	}
	if normalized := normalizeKnownOpenAICodexModel(model); normalized != "" &&
		!strings.EqualFold(strings.TrimSpace(normalized), strings.TrimSpace(model)) {
		return matchGlobalRows(rows, normalizeChannelPricingModelName(normalized))
	}
	return nil
}

func matchGlobalRows(rows []GlobalModelPrice, name string) *ChannelModelPricing {
	var wildcard *GlobalModelPrice
	var wildcardLen int
	for i := range rows {
		entry := &rows[i]
		pattern := normalizeChannelPricingModelName(strings.TrimSpace(entry.ModelPattern))
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := normalizeChannelPricingModelName(strings.TrimSuffix(pattern, "*"))
			if prefix != "" && strings.HasPrefix(name, prefix) && len(prefix) > wildcardLen {
				cp := *entry
				wildcard = &cp
				wildcardLen = len(prefix)
			}
			continue
		}
		if pattern == name {
			out := globalPriceToChannel(entry)
			return &out
		}
	}
	if wildcard == nil {
		return nil
	}
	out := globalPriceToChannel(wildcard)
	return &out
}

// globalPriceToChannel 深拷贝价格指针：快照行与其派生物不共享 *float64，
// 下游任何原地修改都不会污染 60s 快照。
func globalPriceToChannel(p *GlobalModelPrice) ChannelModelPricing {
	mode := p.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	return ChannelModelPricing{
		Models:            []string{p.ModelPattern},
		BillingMode:       mode,
		InputPrice:        copyFloatPtr(p.InputPrice),
		OutputPrice:       copyFloatPtr(p.OutputPrice),
		CacheWritePrice:   copyFloatPtr(p.CacheWritePrice),
		CacheWrite1hPrice: copyFloatPtr(p.CacheWrite1hPrice),
		CacheReadPrice:    copyFloatPtr(p.CacheReadPrice),
		PerRequestPrice:   copyFloatPtr(p.PerRequestPrice),
	}
}

func copyFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

// Get 返回单个全站定价（不存在返回 ErrGlobalModelPricingNotFound）。
func (s *GlobalModelPricingService) Get(ctx context.Context, id int64) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	out, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGlobalModelPricingNotFound
		}
		return nil, err
	}
	return out, nil
}

// List 返回管理端列表（含禁用）。
func (s *GlobalModelPricingService) List(ctx context.Context) ([]GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	return s.repo.ListAll(ctx)
}

// Create 新建全站定价并刷新快照。
func (s *GlobalModelPricingService) Create(ctx context.Context, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	if err := ValidateGlobalModelPriceInput(in); err != nil {
		return nil, err
	}
	in.ModelPattern = strings.ToLower(strings.TrimSpace(in.ModelPattern))
	out, err := s.repo.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	s.Invalidate()
	return out, nil
}

// Update 更新全站定价并刷新快照（id 不存在返回 ErrGlobalModelPricingNotFound）。
func (s *GlobalModelPricingService) Update(ctx context.Context, id int64, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	if err := ValidateGlobalModelPriceInput(in); err != nil {
		return nil, err
	}
	in.ModelPattern = strings.ToLower(strings.TrimSpace(in.ModelPattern))
	out, err := s.repo.Update(ctx, id, in)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGlobalModelPricingNotFound
		}
		return nil, err
	}
	s.Invalidate()
	return out, nil
}

// Delete 删除全站定价并刷新快照。
func (s *GlobalModelPricingService) Delete(ctx context.Context, id int64) error {
	if s == nil || s.repo == nil {
		return errors.New("global pricing service unavailable")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGlobalModelPricingNotFound
		}
		return err
	}
	s.Invalidate()
	return nil
}

// SetEnabled 启停全站定价并刷新快照。
func (s *GlobalModelPricingService) SetEnabled(ctx context.Context, id int64, enabled bool) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	if err := s.repo.SetEnabled(ctx, id, enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGlobalModelPricingNotFound
		}
		return nil, err
	}
	s.Invalidate()
	return s.repo.GetByID(ctx, id)
}
