package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// checkGroupSecurityPolicy 是分组安全策略在网关审计链中的唯一入口，
// 由 runSecurityAudit 在全局审计之前调用。开关关闭（默认）时为纯 no-op。
// 返回 nil 表示放行；非 nil 且 AllowNextStage=false 时调用方必须拒绝请求。
func checkGroupSecurityPolicy(c *gin.Context, securityPolicy *service.SecurityPolicyService, apiKey *service.APIKey, protocol, model string, body []byte) *securityaudit.Decision {
	if securityPolicy == nil || apiKey == nil {
		return nil
	}
	group := apiKey.Group
	if group == nil || !group.SecurityPolicyEnabled {
		return nil
	}
	mode := service.NormalizeSecurityPolicyMode(group.SecurityPolicyMode)
	verdict := securityPolicy.EvaluateRequest(c.Request.Context(), c, service.SecurityPolicyRequest{
		APIKey:    apiKey,
		Protocol:  protocol,
		Model:     model,
		Endpoint:  GetInboundEndpoint(c),
		Body:      body,
		RequestID: c.Writer.Header().Get("X-Request-Id"),
		ClientIP:  strings.TrimSpace(ip.GetClientIP(c)),
		UserAgent: c.GetHeader("User-Agent"),
	})
	if verdict == nil || verdict.Allowed {
		return nil
	}
	// 异步落库/封禁/邮件：本请求已决定拒绝，不阻塞响应。
	record := service.SecurityPolicyHitRecord{
		RequestID:   c.Writer.Header().Get("X-Request-Id"),
		APIKey:      apiKey,
		Protocol:    protocol,
		Model:       model,
		Endpoint:    GetInboundEndpoint(c),
		Verdict:     verdict,
		SessionMode: mode,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		securityPolicy.RecordHit(ctx, record)
	}()
	status := http.StatusForbidden
	if status < 400 || status > 599 {
		status = http.StatusForbidden
	}
	return &securityaudit.Decision{
		Kind:           securityaudit.DecisionBlock,
		HTTPStatus:     status,
		ErrorCode:      verdict.ErrorCode,
		ClientMessage:  verdict.ClientMessage,
		AllowNextStage: false,
	}
}
