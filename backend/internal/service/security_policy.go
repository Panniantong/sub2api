package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// SecurityPolicyModeBlockSession 命中后终止会话：同一会话后续请求直接 403，
// 需新建会话才能继续。
const SecurityPolicyModeBlockSession = "block_session"

// SecurityPolicyModeBlockRequest 命中后仅拦截当次请求，不记录会话封禁。
const SecurityPolicyModeBlockRequest = "block_request"

// NormalizeSecurityPolicyMode 把未知/空模式收敛为 block_session，保证读路径
// 永不因脏配置静默放行。
func NormalizeSecurityPolicyMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), SecurityPolicyModeBlockRequest) {
		return SecurityPolicyModeBlockRequest
	}
	return SecurityPolicyModeBlockSession
}

// SecurityPolicyActionBlock / SecurityPolicyActionSessionBlock 是写入
// content_moderation_logs 的 action。两者恒排除在封号计数之外
// （安全策略只做会话处置，不做用户封禁）。
const (
	SecurityPolicyActionBlock         = "security_policy_block"
	SecurityPolicyActionSessionBlock  = "security_policy_session_block"
	SecurityPolicyActionCategory      = "security_policy"
	SecurityPolicyErrorCodeViolation  = "SECURITY_POLICY_VIOLATION"
	SecurityPolicyErrorCodeTerminated = "SECURITY_POLICY_SESSION_TERMINATED"
)

// SecurityPolicySessionTTL 会话封禁默认有效期：命中后同一会话 24h 内直接拒绝。
const SecurityPolicySessionTTL = 24 * time.Hour

// SecurityPolicyKeyword 是 security_policy_keywords 表的行映射。
type SecurityPolicyKeyword struct {
	ID        int64
	GroupID   *int64
	Keyword   string
	Category  string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SecurityPolicyKeywordInput 是新建自定义词的入参。GroupID 为 nil 表示全局词。
type SecurityPolicyKeywordInput struct {
	GroupID  *int64
	Keyword  string
	Category string
}

// SecurityPolicyKeywordUpdate 是更新自定义词的入参，全 nil 表示无变更。
type SecurityPolicyKeywordUpdate struct {
	Keyword  *string
	Category *string
	Enabled  *bool
}

var (
	// ErrSecurityPolicyKeywordExists 关键词在同一作用域已存在。
	ErrSecurityPolicyKeywordExists = infraerrors.Conflict("SECURITY_POLICY_KEYWORD_EXISTS", "security policy keyword already exists")
	// ErrSecurityPolicyKeywordNotFound 关键词不存在。
	ErrSecurityPolicyKeywordNotFound = infraerrors.NotFound("SECURITY_POLICY_KEYWORD_NOT_FOUND", "security policy keyword not found")
)

// SecurityPolicyRepository 是安全策略关键词仓储接口。
type SecurityPolicyRepository interface {
	// ListEffectiveKeywords 返回对指定分组生效的自定义词（全局 + 本组），仅 enabled。
	ListEffectiveKeywords(ctx context.Context, groupID *int64) ([]SecurityPolicyKeyword, error)
	// ListKeywords 管理端查询：groupID nil = 全部，0 = 仅全局，>0 = 仅指定分组。
	ListKeywords(ctx context.Context, groupID *int64, includeDisabled bool) ([]SecurityPolicyKeyword, error)
	CreateKeyword(ctx context.Context, in SecurityPolicyKeywordInput) (*SecurityPolicyKeyword, error)
	UpdateKeyword(ctx context.Context, id int64, in SecurityPolicyKeywordUpdate) (*SecurityPolicyKeyword, error)
	DeleteKeyword(ctx context.Context, id int64) error
}

// SecurityPolicySessionStore 是会话封禁存储（Redis HASH：scope -> field）。
type SecurityPolicySessionStore interface {
	// MarkSessionBlocked 在 scope 下写入一批封禁 field 并刷新 TTL。
	MarkSessionBlocked(ctx context.Context, scope string, fields []string, ttl time.Duration) error
	// FindBlockedSessionField 按序检查 fields，返回命中的第一个。
	FindBlockedSessionField(ctx context.Context, scope string, fields []string) (string, error)
	// UnblockSessionScope 清除整个 scope（管理端解封）。
	UnblockSessionScope(ctx context.Context, scope string) (bool, error)
}

// SecurityPolicySnapshot 是一次合并后的词表快照：全局集合（内置 seed +
// 全局自定义词）与按组集合（各组自定义词），分别编译 AC 自动机。
type SecurityPolicySnapshot struct {
	Global  *securityPolicyCompiledSet
	ByGroup map[int64]*securityPolicyCompiledSet
}

type securityPolicyCompiledSet struct {
	Keywords   []string
	Categories map[string]string
	Matcher    *contentModerationKeywordMatcher
}

// BuildSecurityPolicySnapshot 合并内置 seed 与自定义词并编译。
// 自定义词按 GroupID 归属分组集合（nil = 全局）；去重大小写不敏感，
// 内置优先保留分类。
func BuildSecurityPolicySnapshot(custom []SecurityPolicyKeyword) *SecurityPolicySnapshot {
	snapshot := &SecurityPolicySnapshot{ByGroup: make(map[int64]*securityPolicyCompiledSet)}
	globalWords := make([]string, 0, len(SecurityPolicyBuiltinKeywords())+len(custom))
	globalCats := make(map[string]string)
	globalSeen := make(map[string]struct{})
	for _, seed := range SecurityPolicyBuiltinKeywords() {
		key := strings.ToLower(strings.TrimSpace(seed.Keyword))
		if key == "" {
			continue
		}
		if _, dup := globalSeen[key]; dup {
			continue
		}
		globalSeen[key] = struct{}{}
		globalWords = append(globalWords, seed.Keyword)
		globalCats[seed.Keyword] = seed.Category
	}
	grouped := make(map[int64]map[string]SecurityPolicyKeyword)
	for _, word := range custom {
		keyword := strings.TrimSpace(word.Keyword)
		key := strings.ToLower(keyword)
		if key == "" {
			continue
		}
		if word.GroupID == nil {
			if _, dup := globalSeen[key]; dup {
				continue
			}
			globalSeen[key] = struct{}{}
			globalWords = append(globalWords, keyword)
			category := strings.TrimSpace(word.Category)
			if category == "" {
				category = SecurityPolicyCategoryCustom
			}
			globalCats[keyword] = category
			continue
		}
		if grouped[*word.GroupID] == nil {
			grouped[*word.GroupID] = make(map[string]SecurityPolicyKeyword)
		}
		if _, dup := globalSeen[key]; dup {
			continue
		}
		if _, dup := grouped[*word.GroupID][key]; dup {
			continue
		}
		grouped[*word.GroupID][key] = word
	}
	snapshot.Global = compileSecurityPolicySet(globalWords, globalCats)
	for groupID, words := range grouped {
		keywords := make([]string, 0, len(words))
		categories := make(map[string]string, len(words))
		for _, word := range words {
			keywords = append(keywords, word.Keyword)
			category := strings.TrimSpace(word.Category)
			if category == "" {
				category = SecurityPolicyCategoryCustom
			}
			categories[word.Keyword] = category
		}
		snapshot.ByGroup[groupID] = compileSecurityPolicySet(keywords, categories)
	}
	return snapshot
}

func compileSecurityPolicySet(keywords []string, categories map[string]string) *securityPolicyCompiledSet {
	return &securityPolicyCompiledSet{
		Keywords:   keywords,
		Categories: categories,
		Matcher:    newContentModerationKeywordMatcher(keywords),
	}
}

// Match 在快照中匹配文本：先全局集合，再本组集合。返回命中的关键词原文与分类。
func (s *SecurityPolicySnapshot) Match(groupID int64, text string) (string, string, bool) {
	if s == nil || strings.TrimSpace(text) == "" {
		return "", "", false
	}
	if s.Global != nil && s.Global.Matcher != nil {
		if keyword, ok := s.Global.Matcher.Match(text); ok {
			return keyword, s.Global.Categories[keyword], true
		}
	}
	if g := s.ByGroup[groupID]; g != nil && g.Matcher != nil {
		if keyword, ok := g.Matcher.Match(text); ok {
			return keyword, g.Categories[keyword], true
		}
	}
	return "", "", false
}
