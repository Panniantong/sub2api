package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// SecurityPolicyModelReviewer 是模型复核通道。*ContentModerationService
// 已实现（ReviewTextForSecurityPolicy），测试用替身即可。
type SecurityPolicyModelReviewer interface {
	ReviewTextForSecurityPolicy(ctx context.Context, text string) (bool, string, error)
}

// SecurityPolicyService 是分组安全策略的运行时服务：本地关键词前置检测、
// 跨协议会话封禁、命中记录与邮件提醒，外加模型复核推翻。开关默认关闭
// （DB 默认 false），未开启分组零行为变化。
type SecurityPolicyService struct {
	reviewer       SecurityPolicyModelReviewer
	keywordRepo    SecurityPolicyRepository
	sessionStore   SecurityPolicySessionStore
	moderationRepo ContentModerationRepository
	settingRepo    SettingRepository
	groupRepo      GroupRepository
	emailService   *EmailService

	snapshot   atomic.Value // *securityPolicyKeywordSnapshot
	snapshotSF singleflight.Group

	// emailThrottleMu/emailThrottle 按收件人节流违规提醒邮件：共享 key 被刷
	// 敏感词时只Limited首封即时送达，冷却窗内命中只落库不重发，避免邮箱轰炸。
	emailThrottleMu sync.Mutex
	emailThrottle   map[string]time.Time
}

// securityPolicyViolationEmailCooldown 同一收件人违规提醒邮件冷却窗。
const securityPolicyViolationEmailCooldown = time.Hour

// securityPolicyEmailThrottleMaxEntries 节流表上限：超限时顺手清掉已过期条目，
// 仍满则拒绝记录（退化为允许发送，避免误拦截正常提醒）。
const securityPolicyEmailThrottleMaxEntries = 100000

type securityPolicyKeywordSnapshot struct {
	at       time.Time
	snapshot *SecurityPolicySnapshot
}

// securityPolicySnapshotTTL 词表快照 TTL：关键词变更最多延迟 60s 生效。
const securityPolicySnapshotTTL = 60 * time.Second

// securityPolicySnapshotErrorTTL 快照加载失败时的退避，避免 DB 故障时每请求重试。
const securityPolicySnapshotErrorTTL = 5 * time.Second

func NewSecurityPolicyService(
	reviewer SecurityPolicyModelReviewer,
	keywordRepo SecurityPolicyRepository,
	sessionStore SecurityPolicySessionStore,
	moderationRepo ContentModerationRepository,
	settingRepo SettingRepository,
	groupRepo GroupRepository,
	emailService *EmailService,
) *SecurityPolicyService {
	return &SecurityPolicyService{
		reviewer:       reviewer,
		keywordRepo:    keywordRepo,
		sessionStore:   sessionStore,
		moderationRepo: moderationRepo,
		settingRepo:    settingRepo,
		groupRepo:      groupRepo,
		emailService:   emailService,
	}
}

// SecurityPolicyRequest 是单次安全策略评估的输入，均来自已认证请求上下文。
type SecurityPolicyRequest struct {
	APIKey    *APIKey
	Protocol  string
	Model     string
	Endpoint  string
	Body      []byte
	RequestID string
	ClientIP  string
	UserAgent string
}

// SecurityPolicyVerdict 是评估结论。Allowed=false 时调用方必须拒绝请求。
type SecurityPolicyVerdict struct {
	Allowed           bool
	SessionTerminated bool // 已终止会话的再次命中（需提示新建会话）
	MatchedKeyword    string
	Category          string
	ErrorCode         string
	ClientMessage     string
	// 以下字段供命中记录使用（RecordHit 入参）。
	SessionKeys []string
	ScopeKey    string
	Excerpt     string
	// ReviewText 归一化后的完整输入文本，供模型复核使用（不落库原文，
	// 仅内存传递；日志仍只存脱敏 excerpt）。
	ReviewText string
}

// EvaluateRequest 执行分组安全策略评估：开关关闭直接放行；先查会话封禁，
// 再做本地关键词匹配。本方法只读（快照缓存 + Redis 查询），不写任何状态。
func (s *SecurityPolicyService) EvaluateRequest(ctx context.Context, c *gin.Context, in SecurityPolicyRequest) *SecurityPolicyVerdict {
	allow := &SecurityPolicyVerdict{Allowed: true}
	if s == nil || in.APIKey == nil {
		return allow
	}
	group := in.APIKey.Group
	if group == nil || !group.SecurityPolicyEnabled {
		return allow
	}
	groupID := group.ID
	apiKeyID := in.APIKey.ID
	scope := SecurityPolicySessionScopeKey(groupID, apiKeyID)
	lookupKeys := SecurityPolicySessionLookupKeys(in.Protocol, apiKeyID, c, in.Body)
	if len(lookupKeys) > 0 && s.sessionStore != nil {
		if field, err := s.sessionStore.FindBlockedSessionField(ctx, scope, lookupKeys); err != nil {
			slog.Warn("security_policy.session_lookup_failed", "group_id", groupID, "api_key_id", apiKeyID, "error", err)
		} else if field != "" {
			return &SecurityPolicyVerdict{
				Allowed:           false,
				SessionTerminated: true,
				ErrorCode:         SecurityPolicyErrorCodeTerminated,
				ClientMessage:     "该会话因违反安全策略已被终止，请新建会话后继续。如有疑问请联系管理员。",
				ScopeKey:          scope,
				SessionKeys:       []string{field},
			}
		}
	}
	input := ExtractContentModerationInput(in.Protocol, in.Body)
	input.Normalize()
	if input.IsEmpty() {
		return allow
	}
	snapshot := s.keywordSnapshot(ctx)
	keyword, category, ok := snapshot.Match(groupID, input.Text)
	if !ok {
		return allow
	}
	return &SecurityPolicyVerdict{
		Allowed:        false,
		MatchedKeyword: keyword,
		Category:       category,
		ErrorCode:      SecurityPolicyErrorCodeViolation,
		ClientMessage:  "请求内容违反本分组安全策略（敏感话题），该请求已被拦截。请调整输入后重试。",
		ScopeKey:       scope,
		SessionKeys:    lookupKeys,
		Excerpt:        trimRunes(redactContentModerationSecrets(input.Text), maxModerationExcerptRunes),
		ReviewText:     input.Text,
	}
}

// SecurityPolicyHitRecord 是命中落库/通知的载荷。
type SecurityPolicyHitRecord struct {
	RequestID   string
	APIKey      *APIKey
	Protocol    string
	Model       string
	Endpoint    string
	Verdict     *SecurityPolicyVerdict
	SessionMode string // block_session / block_request（实际生效的处置）
}

// RecordHit 持久化命中日志；block_session 模式写会话封禁；邮件开关开启时提醒用户。
// 设计为异步调用（handler 侧 go func），内部对各 IO 独立容错。
func (s *SecurityPolicyService) RecordHit(ctx context.Context, in SecurityPolicyHitRecord) {
	if s == nil || in.Verdict == nil || in.APIKey == nil {
		return
	}
	group := in.APIKey.Group
	var groupID *int64
	var groupName string
	if group != nil {
		groupID = &group.ID
		groupName = group.Name
	}
	var userID *int64
	var userEmail string
	if in.APIKey.User != nil {
		if in.APIKey.User.ID > 0 {
			uid := in.APIKey.User.ID
			userID = &uid
		}
		userEmail = strings.TrimSpace(in.APIKey.User.Email)
	}
	var apiKeyID *int64
	if in.APIKey.ID > 0 {
		id := in.APIKey.ID
		apiKeyID = &id
	}
	action := SecurityPolicyActionBlock
	if in.Verdict.SessionTerminated {
		action = SecurityPolicyActionSessionBlock
	}
	category := in.Verdict.Category
	if category == "" {
		category = SecurityPolicyActionCategory
	} else {
		category = SecurityPolicyActionCategory + ":" + category
	}
	log := &ContentModerationLog{
		RequestID:       in.RequestID,
		UserID:          userID,
		UserEmail:       userEmail,
		APIKeyID:        apiKeyID,
		APIKeyName:      in.APIKey.Name,
		GroupID:         cloneInt64Ptr(groupID),
		GroupName:       groupName,
		Endpoint:        in.Endpoint,
		Provider:        securityPolicyProviderOf(in.Protocol),
		Model:           in.Model,
		Mode:            "pre_request",
		Action:          action,
		Flagged:         true,
		HighestCategory: category,
		HighestScore:    1.0,
		MatchedKeyword:  in.Verdict.MatchedKeyword,
		InputExcerpt:    in.Verdict.Excerpt,
		CreatedAt:       time.Now(),
	}
	if s.moderationRepo != nil {
		if err := s.moderationRepo.CreateLog(ctx, log); err != nil {
			slog.Warn("security_policy.create_log_failed", "group_id", groupID, "error", err)
		}
	}
	// block_session 模式：将会话 key 写入封禁，下轮同会话直接拒绝。
	if in.SessionMode == SecurityPolicyModeBlockSession && len(in.Verdict.SessionKeys) > 0 && s.sessionStore != nil {
		markCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.sessionStore.MarkSessionBlocked(markCtx, in.Verdict.ScopeKey, in.Verdict.SessionKeys, SecurityPolicySessionTTL); err != nil {
			slog.Warn("security_policy.mark_session_failed", "scope", in.Verdict.ScopeKey, "error", err)
		}
	}
	// 邮件提醒：分组开关可自由开启/关闭（默认开），无邮箱时静默跳过。
	// 同一收件人冷却窗内只发首封（防共享 key 被刷词轰炸邮箱），超窗恢复。
	if group != nil && group.SecurityPolicyEmailEnabled && userEmail != "" && s.emailService != nil {
		if !s.allowViolationEmail(userEmail, in.APIKey.ID) {
			slog.Info("security_policy.email_throttled", "user_id", userID, "api_key_id", in.APIKey.ID)
		} else if err := s.sendViolationEmail(ctx, log, in.Verdict); err != nil {
			slog.Warn("security_policy.email_failed", "user_id", userID, "error", err)
		} else if s.moderationRepo != nil && log.ID > 0 {
			if err := s.moderationRepo.UpdateLogEmailSent(ctx, log.ID, true); err != nil {
				slog.Warn("security_policy.update_email_sent_failed", "log_id", log.ID, "error", err)
			}
		}
	}
	// 模型复核推翻：仅 block_session 的新鲜命中参与（entry 复访不重复复核）。
	// 复核为 benign → 解封会话 + 标记 overturned；确认/不可用 → 维持本地拦截。
	s.reviewHitAsync(ctx, log, in)
}

// allowViolationEmail 按收件人执行冷却窗节流：窗内返回 false（跳过发送），
// 首封/过窗返回 true 并记录时间。key 优先用邮箱小写，缺失时退化到 apiKeyID。
// map 满且清不掉过期条目时返回 true（退化为允许发送，不误拦截）。
func (s *SecurityPolicyService) allowViolationEmail(userEmail string, apiKeyID int64) bool {
	key := "mail:" + strings.ToLower(strings.TrimSpace(userEmail))
	if key == "mail:" {
		key = fmt.Sprintf("key:%d", apiKeyID)
	}
	now := time.Now()
	s.emailThrottleMu.Lock()
	defer s.emailThrottleMu.Unlock()
	if last, ok := s.emailThrottle[key]; ok && now.Sub(last) < securityPolicyViolationEmailCooldown {
		return false
	}
	if s.emailThrottle == nil {
		s.emailThrottle = make(map[string]time.Time)
	}
	if len(s.emailThrottle) >= securityPolicyEmailThrottleMaxEntries {
		for k, last := range s.emailThrottle {
			if now.Sub(last) >= securityPolicyViolationEmailCooldown {
				delete(s.emailThrottle, k)
			}
		}
		if len(s.emailThrottle) >= securityPolicyEmailThrottleMaxEntries {
			return true
		}
	}
	s.emailThrottle[key] = now
	return true
}

// reviewHitAsync 异步模型复核。本地关键词命中先执行（快），模型结论后到：
func (s *SecurityPolicyService) reviewHitAsync(ctx context.Context, log *ContentModerationLog, in SecurityPolicyHitRecord) {
	if s == nil || s.reviewer == nil || in.Verdict == nil || in.Verdict.SessionTerminated {
		return
	}
	if in.SessionMode != SecurityPolicyModeBlockSession {
		return
	}
	if log == nil || log.ID <= 0 || strings.TrimSpace(in.Verdict.ReviewText) == "" {
		return
	}
	reviewCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	flagged, category, err := s.reviewer.ReviewTextForSecurityPolicy(reviewCtx, in.Verdict.ReviewText)
	if err != nil {
		slog.Warn("security_policy.review_unavailable", "log_id", log.ID, "error", err)
		return
	}
	if flagged {
		slog.Info("security_policy.review_confirmed", "log_id", log.ID, "category", category)
		return
	}
	// 推翻：解封会话 + 标记日志。用户侧无需任何操作，重试即放行。
	var groupID, apiKeyID int64
	if in.APIKey != nil {
		if in.APIKey.Group != nil {
			groupID = in.APIKey.Group.ID
		}
		apiKeyID = in.APIKey.ID
	}
	if unblocked, uerr := s.UnblockSessionScope(reviewCtx, groupID, apiKeyID); uerr != nil {
		slog.Warn("security_policy.overturn_unblock_failed", "log_id", log.ID, "error", uerr)
	} else if !unblocked {
		slog.Info("security_policy.overturned_session_already_clear", "log_id", log.ID)
	}
	if s.moderationRepo != nil {
		if uerr := s.moderationRepo.UpdateLogOverturned(reviewCtx, log.ID); uerr != nil {
			slog.Warn("security_policy.overturn_mark_failed", "log_id", log.ID, "error", uerr)
		}
	}
	slog.Info("security_policy.overturned", "log_id", log.ID,
		"keyword", in.Verdict.MatchedKeyword, "group_id", groupID, "api_key_id", apiKeyID)
}

func securityPolicyProviderOf(protocol string) string {
	switch protocol {
	case ContentModerationProtocolOpenAIResponses,
		ContentModerationProtocolOpenAIChat,
		ContentModerationProtocolOpenAIImages:
		return "openai"
	case ContentModerationProtocolAnthropicMessages:
		return "anthropic"
	case ContentModerationProtocolGemini:
		return "gemini"
	default:
		return strings.TrimSpace(protocol)
	}
}

func (s *SecurityPolicyService) sendViolationEmail(ctx context.Context, log *ContentModerationLog, verdict *SecurityPolicyVerdict) error {
	siteName := s.siteName(ctx)
	variables := map[string]string{
		"triggered_at":    log.CreatedAt.UTC().Format(time.RFC3339),
		"model":           defaultContentModerationString(log.Model, "-"),
		"group_name":      defaultContentModerationString(log.GroupName, "-"),
		"matched_keyword": defaultContentModerationString(verdict.MatchedKeyword, "-"),
	}
	if s.emailService.notificationEmailService != nil {
		err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventSecurityPolicyNotice,
			RecipientEmail: log.UserEmail,
			RecipientName:  emailRecipientName(log.UserEmail),
			UserID:         contentModerationEmailUserID(log),
			SourceType:     "security_policy",
			SourceID:       contentModerationEmailSourceID(log),
			Variables:      variables,
		})
		if err == nil {
			return nil
		}
		if !shouldFallbackNotificationEmail(err) {
			return err
		}
		slog.Warn("template security policy email failed; falling back", "err", err.Error())
	}
	subject := fmt.Sprintf("[%s] 安全策略提醒 / Security Policy Notice", sanitizeEmailHeader(siteName))
	return s.emailService.SendEmail(ctx, log.UserEmail, subject, buildSecurityPolicyNoticeEmailBody(siteName, log, verdict))
}

func buildSecurityPolicyNoticeEmailBody(siteName string, log *ContentModerationLog, verdict *SecurityPolicyVerdict) string {
	var sb strings.Builder
	sb.WriteString("您好 " + emailRecipientName(log.UserEmail) + "：\n\n")
	sb.WriteString("您在分组「" + log.GroupName + "」的一次请求触发了安全策略（敏感话题），该请求已被拦截。\n")
	if verdict.MatchedKeyword != "" {
		sb.WriteString("命中关键词：" + verdict.MatchedKeyword + "\n")
	}
	if verdict.SessionTerminated {
		sb.WriteString("该会话已被终止，请新建会话后继续。\n")
	}
	sb.WriteString("\n如认为系误判，请调整输入措辞后重试，或联系管理员。\n")
	sb.WriteString("\n—— " + siteName)
	return sb.String()
}

// UnblockSessionScope 管理端解封：清除指定分组+Key 的全部会话封禁。
func (s *SecurityPolicyService) UnblockSessionScope(ctx context.Context, groupID, apiKeyID int64) (bool, error) {
	if s == nil || s.sessionStore == nil {
		return false, nil
	}
	return s.sessionStore.UnblockSessionScope(ctx, SecurityPolicySessionStoreKeyForAdmin(groupID, apiKeyID))
}

// GetBuiltinKeywords 返回内置 seed 词包（管理端展示/审计用）。
func (s *SecurityPolicyService) GetBuiltinKeywords() []SecurityPolicyKeywordSeed {
	return SecurityPolicyBuiltinKeywords()
}

// ListKeywords 管理端查询自定义词。
func (s *SecurityPolicyService) ListKeywords(ctx context.Context, groupID *int64, includeDisabled bool) ([]SecurityPolicyKeyword, error) {
	if s == nil || s.keywordRepo == nil {
		return []SecurityPolicyKeyword{}, nil
	}
	return s.keywordRepo.ListKeywords(ctx, groupID, includeDisabled)
}

// CreateKeyword 新建自定义词。groupID 非 nil 时必须指向存在的分组。
func (s *SecurityPolicyService) CreateKeyword(ctx context.Context, in SecurityPolicyKeywordInput) (*SecurityPolicyKeyword, error) {
	if s == nil || s.keywordRepo == nil {
		return nil, fmt.Errorf("security policy service unavailable")
	}
	keyword := strings.TrimSpace(in.Keyword)
	if keyword == "" {
		return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_KEYWORD", "keyword must not be empty")
	}
	if len([]rune(keyword)) > 200 {
		return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_KEYWORD", "keyword must not exceed 200 runes")
	}
	category := strings.TrimSpace(in.Category)
	if category == "" {
		category = SecurityPolicyCategoryCustom
	}
	if len([]rune(category)) > 50 {
		return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_CATEGORY", "category must not exceed 50 runes")
	}
	if in.GroupID != nil && *in.GroupID > 0 && s.groupRepo != nil {
		if _, err := s.groupRepo.GetByIDLite(ctx, *in.GroupID); err != nil {
			return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_GROUP", "group not found")
		}
	}
	created, err := s.keywordRepo.CreateKeyword(ctx, SecurityPolicyKeywordInput{
		GroupID:  in.GroupID,
		Keyword:  keyword,
		Category: category,
	})
	if err != nil {
		return nil, err
	}
	s.refreshSnapshotAsync()
	return created, nil
}

// UpdateKeyword 更新自定义词（词/分类/启用）。
func (s *SecurityPolicyService) UpdateKeyword(ctx context.Context, id int64, in SecurityPolicyKeywordUpdate) (*SecurityPolicyKeyword, error) {
	if s == nil || s.keywordRepo == nil {
		return nil, fmt.Errorf("security policy service unavailable")
	}
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_KEYWORD_ID", "invalid keyword id")
	}
	if in.Keyword != nil {
		keyword := strings.TrimSpace(*in.Keyword)
		if keyword == "" {
			return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_KEYWORD", "keyword must not be empty")
		}
		if len([]rune(keyword)) > 200 {
			return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_KEYWORD", "keyword must not exceed 200 runes")
		}
		in.Keyword = &keyword
	}
	if in.Category != nil {
		category := strings.TrimSpace(*in.Category)
		if category == "" {
			category = SecurityPolicyCategoryCustom
		}
		if len([]rune(category)) > 50 {
			return nil, infraerrors.BadRequest("INVALID_SECURITY_POLICY_CATEGORY", "category must not exceed 50 runes")
		}
		in.Category = &category
	}
	updated, err := s.keywordRepo.UpdateKeyword(ctx, id, in)
	if err != nil {
		return nil, err
	}
	s.refreshSnapshotAsync()
	return updated, nil
}

// DeleteKeyword 软删除自定义词。
func (s *SecurityPolicyService) DeleteKeyword(ctx context.Context, id int64) error {
	if s == nil || s.keywordRepo == nil {
		return fmt.Errorf("security policy service unavailable")
	}
	if id <= 0 {
		return infraerrors.BadRequest("INVALID_SECURITY_POLICY_KEYWORD_ID", "invalid keyword id")
	}
	if err := s.keywordRepo.DeleteKeyword(ctx, id); err != nil {
		return err
	}
	s.refreshSnapshotAsync()
	return nil
}

// refreshSnapshotAsync 使词表快照尽快过期（下次评估重建，最多延迟一个 TTL）。
func (s *SecurityPolicyService) refreshSnapshotAsync() {
	if s == nil {
		return
	}
	s.snapshot.Store(&securityPolicyKeywordSnapshot{at: time.Now().Add(-securityPolicySnapshotTTL), snapshot: nil})
}

// keywordSnapshot 返回合并词表快照（60s TTL + 失败退避）。仓储不可用时退化为纯内置表。
func (s *SecurityPolicyService) keywordSnapshot(ctx context.Context) *SecurityPolicySnapshot {
	if s == nil {
		return BuildSecurityPolicySnapshot(nil)
	}
	if cached, ok := s.snapshot.Load().(*securityPolicyKeywordSnapshot); ok && cached != nil {
		if time.Since(cached.at) < securityPolicySnapshotTTL {
			return cached.snapshot
		}
	}
	result, _, _ := s.snapshotSF.Do("security_policy_keywords", func() (any, error) {
		if cached, ok := s.snapshot.Load().(*securityPolicyKeywordSnapshot); ok && cached != nil {
			if time.Since(cached.at) < securityPolicySnapshotTTL {
				return cached.snapshot, nil
			}
		}
		var custom []SecurityPolicyKeyword
		if s.keywordRepo != nil {
			dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// 全量 enabled 词（全局 + 各组），按组拆分在快照内完成。
			words, err := s.keywordRepo.ListKeywords(dbCtx, nil, false)
			if err != nil {
				slog.Warn("security_policy.keywords_load_failed", "error", err)
			} else {
				custom = words
			}
		}
		snapshot := BuildSecurityPolicySnapshot(custom)
		s.snapshot.Store(&securityPolicyKeywordSnapshot{at: time.Now(), snapshot: snapshot})
		return snapshot, nil
	})
	if snapshot, ok := result.(*SecurityPolicySnapshot); ok && snapshot != nil {
		return snapshot
	}
	return BuildSecurityPolicySnapshot(nil)
}

// siteName 读取站点名（邮件标题用），失败时回退默认。
func (s *SecurityPolicyService) siteName(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return "Sub2API"
	}
	name, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || strings.TrimSpace(name) == "" {
		return "Sub2API"
	}
	return strings.TrimSpace(name)
}

// ReviewTextForSecurityPolicy 以纯模型复核文本是否违规，绕过关键词/
// 分组/采样门控。无可用配置或调用失败时返回 err，调用方保留本地拦截。
// 与 ContentModerationService 同包，直接复用其快照与审计通道。
func (s *ContentModerationService) ReviewTextForSecurityPolicy(ctx context.Context, text string) (bool, string, error) {
	if s == nil {
		return false, "", fmt.Errorf("content moderation service unavailable")
	}
	if strings.TrimSpace(text) == "" {
		return false, "", nil
	}
	snapshot, err := s.loadRuntimeSnapshot(ctx)
	if err != nil {
		return false, "", err
	}
	cfg := snapshot.config
	if len(cfg.apiKeys()) == 0 {
		return false, "", fmt.Errorf("no moderation api key available")
	}
	result, err := s.callModeration(ctx, cfg, text, true)
	if err != nil {
		return false, "", err
	}
	flagged, category, _ := evaluateModerationScores(result.CategoryScores, cfg.Thresholds)
	return flagged, category, nil
}
