package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	codexWorkModeContextKey      = "openai_codex_work_mode"
	codexWorkModeClientModelKey  = "openai_codex_work_mode_client_model"
	codexWorkModeOriginator      = "codex_work_desktop"
	codexWorkModeClientName      = "Codex Desktop"
	codexWorkModeUserAgentSuffix = " (Mac OS 26.5.1; arm64) unknown (Codex Desktop; 26.527.60818)"
)

// CodexWorkModeDecision separates the client-visible model from the canonical
// model used for channel/account matching and upstream billing.
type CodexWorkModeDecision struct {
	RoutingModel string
	Enabled      bool
	Explicit     bool
}

// ResolveCodexWorkMode resolves the four supported GPT-5.6 names and their
// explicit -wm variants. Unknown models are returned unchanged and never opt in.
func ResolveCodexWorkMode(model string, disabled bool) CodexWorkModeDecision {
	original := strings.TrimSpace(model)
	canonical := canonicalizeOpenAIModelAliasSpelling(original)
	explicit := strings.HasSuffix(canonical, "-wm")
	if explicit {
		canonical = strings.TrimSuffix(canonical, "-wm")
	}

	base := ""
	switch canonical {
	case "gpt-5.6":
		base = "gpt-5.6-sol"
	case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
		base = canonical
	default:
		return CodexWorkModeDecision{RoutingModel: original}
	}

	return CodexWorkModeDecision{
		RoutingModel: base,
		Enabled:      explicit || !disabled,
		Explicit:     explicit,
	}
}

// BindCodexWorkMode records the per-request decision after the handler has
// ruled out image-generation intent. The marker is consumed only by OAuth
// upstream builders; API-key requests remain untouched.
func BindCodexWorkMode(c *gin.Context, enabled bool) {
	if c == nil {
		return
	}
	c.Set(codexWorkModeContextKey, enabled)
}

// BindCodexWorkModeClientModel preserves an explicit -wm name for downstream
// response and requested_model semantics after the upstream body is normalized.
func BindCodexWorkModeClientModel(c *gin.Context, model string) {
	if c == nil {
		return
	}
	c.Set(codexWorkModeClientModelKey, strings.TrimSpace(model))
}

func codexWorkModeClientModel(c *gin.Context, fallback string) string {
	if c == nil {
		return fallback
	}
	value, ok := c.Get(codexWorkModeClientModelKey)
	if !ok {
		return fallback
	}
	model, _ := value.(string)
	if model = strings.TrimSpace(model); model != "" {
		return model
	}
	return fallback
}

func isCodexWorkModeRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	enabled, ok := c.Get(codexWorkModeContextKey)
	if !ok {
		return false
	}
	value, _ := enabled.(bool)
	return value
}

func codexWorkModeIdentity() codexOutboundIdentity {
	version := codexClientVersionFromUA(codexCanonicalUserAgent())
	return codexOutboundIdentity{
		userAgent:  codexWorkModeClientName + "/" + version + codexWorkModeUserAgentSuffix,
		originator: codexWorkModeOriginator,
		version:    version,
	}
}

// applyCodexWorkModeIdentity must run after the normal Codex identity
// enforcement. codex_work_desktop is a special upstream bucket and deliberately
// does not pair with the User-Agent first segment like ordinary Codex clients.
func applyCodexWorkModeIdentity(c *gin.Context, account *Account, h http.Header) {
	if account == nil || account.Type != AccountTypeOAuth || h == nil || !isCodexWorkModeRequest(c) {
		return
	}
	identity := codexWorkModeIdentity()
	h.Set("user-agent", identity.userAgent)
	h.Set("originator", identity.originator)
	h.Set("version", identity.version)
}
