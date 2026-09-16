package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"log/slog"
)

const requestIntegrityModeKey = "request_integrity_mode"

// Identity/TLS and request integrity are independent controls. Existing mode1
// supplies the default; an administrator can explicitly turn the check off.
func (a *Account) RequestIntegrityMode() string {
	if a == nil || a.Platform != PlatformOpenAI {
		return "off"
	}
	if mode, ok := a.Extra[requestIntegrityModeKey].(string); ok {
		switch mode {
		case "off", "observe", "enforce":
			return mode
		}
	}
	if isMode1ProtectionEnabled(a) {
		return "enforce"
	}
	if isOpenAIOAuthLike(a) && a.AntiDegradationEnabled() {
		return "observe"
	}
	return "off"
}

func validateRequestIntegrityExtra(extra map[string]any) error {
	value, present := extra[requestIntegrityModeKey]
	if !present || value == nil {
		return nil
	}
	if mode, ok := value.(string); ok && (mode == "off" || mode == "observe" || mode == "enforce") {
		return nil
	}
	return infraerrors.BadRequest("REQUEST_INTEGRITY_INVALID", "请求完整性模式必须为关闭、仅观察或严格拦截")
}

func checkAccountRequestIntegrity(c *gin.Context, a *Account, original, forwarded []byte) error {
	mode := a.RequestIntegrityMode()
	if mode == "off" {
		return nil
	}
	err := validateMode1RequestIntegrityForAccount(a, original, forwarded)
	if err == nil {
		return nil
	}
	if c != nil {
		c.Set("request_integrity_difference", true)
	}
	// Error text is produced by the validator and contains field names only;
	// never record request bodies, credentials or conversation content here.
	slog.Warn("account_request_integrity_difference", "account_id", a.ID, "mode", mode, "error", err.Error())
	if mode == "enforce" {
		return err
	}
	return nil
}
