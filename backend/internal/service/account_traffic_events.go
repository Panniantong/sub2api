package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Classify the outcome before treating a terminal frame as successful. Some
// providers report failures inside response.done rather than response.failed.
func accountTrafficEventStatus(payload []byte) (int, bool) {
	root := gjson.ParseBytes(payload)
	kind := root.Get("type").String()
	responseStatus := root.Get("response.status").String()
	failed := kind == "error" || kind == "response.failed" || responseStatus == "failed"
	if failed {
		for _, path := range []string{"error", "response.error", "response.status_details.error"} {
			e := root.Get(path)
			for _, field := range []string{"status", "status_code"} {
				if status := int(e.Get(field).Int()); status == 429 || (status >= 500 && status < 600) {
					return status, true
				}
			}
			code := strings.ToLower(e.Get("code").String() + " " + e.Get("type").String())
			if strings.Contains(code, "rate_limit") || strings.Contains(code, "usage_limit_reached") {
				return 429, true
			}
			if strings.Contains(code, "server_error") || strings.Contains(code, "overload") || strings.Contains(code, "internal_error") || strings.Contains(code, "upstream_error") {
				return 503, true
			}
		}
		return 499, true
	}
	switch kind {
	case "response.incomplete", "response.cancelled", "response.canceled":
		return 499, true
	case "response.completed", "response.done":
		if responseStatus != "" && responseStatus != "completed" {
			return 499, true
		}
		return 200, true
	case "message_stop":
		return 200, true
	}
	if root.Get("candidates.0.finishReason").String() == "STOP" || root.Get("choices.0.finish_reason").String() == "stop" {
		return 200, true
	}
	return 0, false
}
