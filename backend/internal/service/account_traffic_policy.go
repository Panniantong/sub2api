package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AccountTrafficPolicyKey = "account_traffic_control"

type AccountTrafficPolicy struct {
	StrictRPMEnabled     bool   `json:"strict_rpm_enabled"`
	RPM                  int    `json:"rpm"`
	Burst                int    `json:"burst"`
	AdaptiveEnabled      bool   `json:"adaptive_enabled"`
	AdaptiveMode         string `json:"adaptive_mode"`
	MinConcurrency       int    `json:"min_concurrency"`
	FailureThreshold     int    `json:"failure_threshold"`
	FailureWindowSeconds int    `json:"failure_window_seconds"`
	RecoverySeconds      int    `json:"recovery_seconds"`
}

func DefaultAccountTrafficPolicy() AccountTrafficPolicy {
	return AccountTrafficPolicy{RPM: 60, Burst: 5, AdaptiveMode: "observe", MinConcurrency: 1, FailureThreshold: 3, FailureWindowSeconds: 60, RecoverySeconds: 60}
}

func ParseAccountTrafficPolicy(extra map[string]any) (AccountTrafficPolicy, error) {
	p := DefaultAccountTrafficPolicy()
	value, exists := extra[AccountTrafficPolicyKey]
	if !exists || value == nil {
		return p, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return p, err
	}
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, infraerrors.BadRequest("ACCOUNT_TRAFFIC_INVALID", "流量控制配置格式无效")
	}
	if p.RPM < 1 || p.RPM > 60000 || p.Burst < 1 || p.Burst > p.RPM || p.MinConcurrency < 1 || p.MinConcurrency > 10000 || p.FailureThreshold < 1 || p.FailureThreshold > 100 || p.FailureWindowSeconds < 10 || p.FailureWindowSeconds > 3600 || p.RecoverySeconds < 10 || p.RecoverySeconds > 3600 || (p.AdaptiveMode != "observe" && p.AdaptiveMode != "automatic") {
		return p, infraerrors.BadRequest("ACCOUNT_TRAFFIC_INVALID", "请检查 RPM、突发额度、并发下限、失败阈值与恢复周期；突发额度不能大于 RPM")
	}
	return p, nil
}

func (p AccountTrafficPolicy) Enabled() bool { return p.StrictRPMEnabled || p.AdaptiveEnabled }

func (p AccountTrafficPolicy) Enforces() bool {
	return p.StrictRPMEnabled || (p.AdaptiveEnabled && p.AdaptiveMode == "automatic")
}

type AccountTrafficPlan struct {
	AccountID int64
	Revision  int64
	Signature string
	HardLimit int
	Policy    AccountTrafficPolicy
}

func AccountTrafficPlanFor(a *Account) (AccountTrafficPlan, error) {
	if a == nil {
		return AccountTrafficPlan{}, nil
	}
	p, err := ParseAccountTrafficPolicy(a.Extra)
	if err != nil {
		return AccountTrafficPlan{}, err
	}
	limit := a.Mode1EffectiveConcurrency()
	if p.AdaptiveEnabled && limit <= 0 {
		return AccountTrafficPlan{}, infraerrors.BadRequest("ACCOUNT_TRAFFIC_INVALID", "启用自适应并发前，请先设置正数的账号并发上限")
	}
	encoded, _ := json.Marshal(struct {
		Policy AccountTrafficPolicy
		Limit  int
	}{p, limit})
	revision := int64(0)
	if !a.UpdatedAt.IsZero() {
		revision = a.UpdatedAt.UnixMicro()
	}
	return AccountTrafficPlan{AccountID: a.ID, Revision: revision, Signature: fmt.Sprintf("%x", sha256.Sum256(encoded)), HardLimit: limit, Policy: p}, nil
}

type accountTrafficPlanKey struct{}
type accountTrafficCoveredKey struct{}
type accountTrafficConfigErrorKey struct{}

// Attach only safe policy metadata, never account credentials. GET discovery
// and status polling are outside the model-request budget.
func WithAccountTrafficRequest(req *http.Request, a *Account) *http.Request {
	if req == nil || req.Method == http.MethodGet || req.Method == http.MethodHead {
		return req
	}
	if a == nil || a.Extra[AccountTrafficPolicyKey] == nil {
		return req
	}
	plan, err := AccountTrafficPlanFor(a)
	if err != nil {
		return req.WithContext(context.WithValue(req.Context(), accountTrafficConfigErrorKey{}, err))
	}
	if !plan.Policy.Enabled() {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), accountTrafficPlanKey{}, plan))
}

type AccountTrafficState struct {
	EffectiveConcurrency   int        `json:"effective_concurrency"`
	RecommendedConcurrency int        `json:"recommended_concurrency"`
	InFlight               int        `json:"in_flight"`
	RequestsLastMinute     int        `json:"requests_last_minute"`
	Accepted               int64      `json:"accepted"`
	RejectedRPM            int64      `json:"rejected_rpm"`
	RejectedConcurrency    int64      `json:"rejected_concurrency"`
	Upstream429            int64      `json:"upstream_429"`
	Upstream5xx            int64      `json:"upstream_5xx"`
	Completed              int64      `json:"completed"`
	AverageDurationMS      float64    `json:"average_duration_ms"`
	LastAdjustmentAt       *time.Time `json:"last_adjustment_at,omitempty"`
}

type AccountTrafficAdmission struct {
	Allowed         bool
	SkipObservation bool
	Reason          string
	RetryAfter      time.Duration
	State           AccountTrafficState
}

type AccountTrafficCache interface {
	Acquire(context.Context, AccountTrafficPlan, string) (AccountTrafficAdmission, error)
	Refresh(context.Context, int64, string) (bool, error)
	Finish(context.Context, AccountTrafficPlan, string, int, int64) error
	Snapshot(context.Context, AccountTrafficPlan) (AccountTrafficState, error)
	Sync(context.Context, AccountTrafficPlan) error
}

type AccountTrafficProvider interface{ AccountTrafficController() *AccountTrafficService }

type AccountTrafficLimitError struct {
	Reason     string
	RetryAfter time.Duration
	Status     int
}

func (e *AccountTrafficLimitError) Error() string { return "ACCOUNT_TRAFFIC_LIMIT: " + e.Reason }

func (e *AccountTrafficLimitError) FailoverError() *UpstreamFailoverError {
	status := e.Status
	if status == 0 {
		status = 429
	}
	seconds := int64((e.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	errorType := AccountTrafficErrorType(status)
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"type": errorType, "code": "account_traffic_limit", "message": e.Reason}})
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: http.Header{"Retry-After": []string{fmt.Sprint(seconds)}}, RequestScopedTransient: true, Scope: GatewayFailureScopeRequest, Reason: "account_traffic_limit", NextAccountAction: NextAccountStop, ClientStatusCode: status, ClientMessage: e.Reason}
}
func (e *AccountTrafficLimitError) As(target any) bool {
	if p, ok := target.(**UpstreamFailoverError); ok {
		*p = e.FailoverError()
		return true
	}
	return false
}

func AccountTrafficErrorType(status int) string {
	if status == http.StatusBadRequest {
		return "invalid_request_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "rate_limit_error"
}
