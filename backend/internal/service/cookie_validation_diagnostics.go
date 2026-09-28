package service

import (
	"context"
	"fmt"
	"time"
)

func (s *OpenAIGatewayService) cooldownCookieValidationFailure(ctx context.Context, account *Account, settings *OpenAICookieSettings, base OpenAICookieAcquisitionLog, message string) {
	seconds := 120
	if settings != nil && settings.CookieHostValidationFailureCooldownSeconds > 0 {
		seconds = settings.CookieHostValidationFailureCooldownSeconds
	}
	// Persist even when the validation context timed out.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := s.markOpenAICodexCookieHostCooldown(persistCtx, account, base.Host, seconds)
	base.BindingStatus = "cooldown"
	if err != nil {
		base.BindingStatus = "cooldown_save_failed"
		message += "；冷却保存失败：" + err.Error()
	} else {
		message += fmt.Sprintf("；账号与该 Host 冷却 %d 秒，至 %s", seconds, openAICodexCookieHostCooldownUntil(account, base.Host).Format(time.RFC3339))
	}
	s.appendOpenAICookieValidationStage(persistCtx, base, "validation_failed", message)
}

// Keep raw upstream responses and credentials out of the live log summary.
// The administrator can inspect the separately retained validation response.
func cookieValidationFailureSummary(result string, status int) string {
	if status == 0 {
		return "验证未获得 HTTP 响应（请求准备、网络或超时错误）"
	}
	if result == "read_error" {
		return fmt.Sprintf("HTTP %d：验证响应读取失败或超时", status)
	}
	if result == "response_too_large" {
		return fmt.Sprintf("HTTP %d：验证响应超过 1 MiB，未判定通过", status)
	}
	switch status {
	case 400:
		return "HTTP 400：验证请求参数被拒绝"
	case 401:
		return "HTTP 401：验证鉴权失败"
	case 403:
		return "HTTP 403：访问被拒绝，检查账号权限或风控响应"
	case 404:
		return "HTTP 404：验证端点或模型不可用"
	case 429:
		return "HTTP 429：验证被限流或额度不足"
	}
	if status < 200 || status >= 300 {
		return fmt.Sprintf("HTTP %d：上游验证请求失败", status)
	}
	return fmt.Sprintf("HTTP %d：未得到有效 yes/no（空响应、流错误或回答格式不符）", status)
}
