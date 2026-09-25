package service

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
)

func classifyAccountOpsFailure(platform string, status int, headers http.Header, body []byte) (string, string) {
	if status < 400 || status > 599 {
		return "", ""
	}
	if platform == PlatformOpenAI && status == 429 {
		for _, window := range []string{"primary", "secondary"} {
			minutes, _ := strconv.Atoi(headers.Get("x-codex-" + window + "-window-minutes"))
			used, _ := strconv.ParseFloat(headers.Get("x-codex-"+window+"-used-percent"), 64)
			if minutes == 10080 && used >= 100 && !math.IsInf(used, 0) {
				return "weekly_quota", "codex_weekly_header"
			}
		}
	}
	if platform == PlatformAnthropic && status == 429 && strings.EqualFold(headers.Get("anthropic-ratelimit-unified-7d-status"), "rejected") {
		return "weekly_quota", "anthropic_weekly_header"
	}
	if len(body) > 32*1024 {
		return "", ""
	}
	if isOpenAIImagesInsufficientBalance(body) {
		return "balance_low", "balance_error_code"
	}
	var envelope struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	message := ""
	code := ""
	if json.Unmarshal(body, &envelope) == nil {
		code = envelope.Code
		message = envelope.Message
		var detail struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &detail) == nil {
			if detail.Code != "" {
				code = detail.Code
			} else if detail.Type != "" {
				code = detail.Type
			}
			if detail.Message != "" {
				message = detail.Message
			}
		}
		if message == "" {
			_ = json.Unmarshal(envelope.Error, &message)
		}
	} else if !strings.Contains(string(body), "<") {
		message = string(body)
	}
	switch strings.ToLower(code) {
	case "insufficient_balance", "balance_not_enough", "insufficient_credit", "credit_balance_too_low", "account_balance_insufficient", "balance_exhausted":
		return "balance_low", "balance_error_code"
	case "weekly_limit_exceeded", "weekly_quota_exceeded", "weekly_quota_exhausted", "weekly_usage_limit_reached":
		return "weekly_quota", "weekly_error_code"
	}
	text := strings.ToLower(message)
	if strings.Contains(text, "余额不足") || strings.Contains(text, "余额已用尽") || strings.Contains(text, "余额耗尽") || strings.Contains(text, "账户欠费") || strings.Contains(text, "账号欠费") {
		return "balance_low", "balance_error_message"
	}
	for _, phrase := range []string{"insufficient balance", "insufficient credit balance", "credit balance is too low", "balance is insufficient", "balance has been exhausted", "balance depleted", "not enough balance", "balance is not enough", "no enough balance"} {
		if strings.Contains(text, phrase) {
			return "balance_low", "balance_error_message"
		}
	}
	weekly := false
	for _, phrase := range []string{"weekly limit", "weekly quota", "weekly usage limit", "周额度", "周限额", "周使用额度"} {
		if strings.Contains(text, phrase) {
			weekly = true
		}
	}
	if weekly {
		for _, phrase := range []string{"exceeded", "exhausted", "reached", "hit", "已用尽", "耗尽", "已达", "超出", "达到"} {
			if strings.Contains(text, phrase) {
				return "weekly_quota", "weekly_error_message"
			}
		}
	}
	return "", ""
}
