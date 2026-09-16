package service

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const intelligentTestIdentityKey = "intelligent_test_identity"

type intelligentTestIdentity struct {
	sessionID string
	threadID  string
}

// Connectivity and capability tests share the gateway's configured identity.
// Independent probes get fresh source sessions; session/full strategies then
// apply their explicit convergence rules to those sessions.
func prepareIntelligentTestProtection(c *gin.Context, account *Account, payload map[string]any) error {
	if c == nil || c.Request == nil || !account.AntiDegradationEnabled() {
		return nil
	}
	if err := ValidateAccountProtectionConfiguration(account); err != nil {
		return err
	}
	if !isOpenAIOAuthLike(account) || account.GetCodexFingerprintMode() == codexFingerprintOff {
		return nil
	}
	if _, err := resolveMode1TLSProfile(account); err != nil {
		return err
	}
	original, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	stageMode1Request(c, account, original)
	rawSession := uuid.NewString()
	ids := resolveCodexFingerprintIDs(account, rawSession, account.GetCodexFingerprintMode())
	stageCodexFingerprintIDs(c, ids)
	identity := intelligentTestIdentity{
		sessionID: isolateOpenAIUpstreamSessionID(0, account, rawSession),
		threadID:  scopeCodexAccountIdentityValue(account, 0, "thread", rawSession),
	}
	if ids != nil && ids.mode != codexFingerprintDevice {
		identity.sessionID, identity.threadID = ids.sessionID, ids.threadID
	}
	c.Request.Header.Set("session-id", rawSession)
	c.Set(intelligentTestIdentityKey, identity)
	applyStagedCodexFingerprintClientMetadata(c, account, payload)
	metadata, _ := payload["client_metadata"].(map[string]any)
	metadata["session_id"] = identity.sessionID
	metadata["thread_id"] = identity.threadID
	return nil
}

func applyIntelligentTestProtection(c *gin.Context, account *Account, headers http.Header, payload []byte) error {
	if c == nil {
		return nil
	}
	value, exists := c.Get(intelligentTestIdentityKey)
	identity, ok := value.(intelligentTestIdentity)
	if !exists || !ok {
		return nil
	}
	// Apply after account header overrides, matching the protected gateway.
	applyStagedCodexFingerprintHeaders(c, account, headers)
	headers.Set("conversation_id", identity.sessionID)
	headers.Set("thread-id", identity.threadID)
	return validateMode1StagedRequest(c, account, payload)
}
