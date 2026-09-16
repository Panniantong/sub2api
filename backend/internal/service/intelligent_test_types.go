package service

import (
	"context"
	"time"
)

type IntelligentTestConfig struct {
	AnswerType     string                  `json:"answer_type,omitempty"`
	AnswerUnit     string                  `json:"answer_unit,omitempty"`
	AnswerUnitMode string                  `json:"answer_unit_mode,omitempty"`
	AnswerFormat   string                  `json:"answer_format,omitempty"`
	Execution      *ProtectionRuntimeState `json:"execution,omitempty"`
	Prompt         string                  `json:"prompt"`
	Model          string                  `json:"model"`
	Evaluator      string                  `json:"evaluator"`
	ExpectedAnswer string                  `json:"expected_answer"`
	TimeoutSeconds int                     `json:"timeout_seconds"`
}
type IntelligentTestSetting struct {
	TestType    string                `json:"test_type"`
	Name        string                `json:"name"`
	Enabled     bool                  `json:"enabled"`
	UserVisible bool                  `json:"user_visible"`
	Config      IntelligentTestConfig `json:"config"`
	UpdatedAt   time.Time             `json:"updated_at"`
}
type IntelligentTestRecord struct {
	AvailableAt     *time.Time             `json:"available_at,omitempty"`
	QueueReason     string                 `json:"queue_reason,omitempty"`
	ID              int64                  `json:"id"`
	AccountID       int64                  `json:"account_id"`
	TestType        string                 `json:"test_type"`
	Status          string                 `json:"status"`
	Score           *float64               `json:"score"`
	Result          string                 `json:"result"`
	ResultImage     string                 `json:"result_image"`
	Input           string                 `json:"input,omitempty"`
	RawResponse     string                 `json:"raw_response,omitempty"`
	RawTruncated    bool                   `json:"raw_truncated"`
	ErrorMessage    string                 `json:"error_message"`
	DurationMS      int64                  `json:"duration_ms"`
	Model           string                 `json:"model"`
	AntiDegradation bool                   `json:"anti_degradation"`
	ConfigSnapshot  *IntelligentTestConfig `json:"config_snapshot,omitempty"`
	Evaluation      map[string]any         `json:"evaluation,omitempty"`
	StartedAt       *time.Time             `json:"started_at"`
	FinishedAt      *time.Time             `json:"finished_at"`
	CreatedAt       time.Time              `json:"created_at"`
	LeaseToken      string                 `json:"-"`
}
type IntelligentTestCardTest struct {
	LatestCompleted      *IntelligentTestRecord `json:"latest_completed,omitempty"`
	TestType             string                 `json:"test_type"`
	Latest               *IntelligentTestRecord `json:"latest"`
	HistoryCount         int64                  `json:"history_count"`
	ConsecutiveAnomalies int                    `json:"consecutive_anomalies"`
	Risk                 string                 `json:"risk"`
}
type IntelligentTestAccountCard struct {
	AccountID       int64                     `json:"account_id"`
	Name            string                    `json:"name"`
	Notes           string                    `json:"notes"`
	Platform        string                    `json:"platform"`
	AccountType     string                    `json:"account_type"`
	AccountStatus   string                    `json:"account_status"`
	GroupIDs        []int64                   `json:"group_ids"`
	AntiDegradation bool                      `json:"anti_degradation"`
	Tests           []IntelligentTestCardTest `json:"tests"`
}
type IntelligentTestOverview struct {
	ReviewAccounts       int64 `json:"review_accounts"`
	TotalAccounts        int64 `json:"total_accounts"`
	TestedToday          int64 `json:"tested_today"`
	SuccessAccounts      int64 `json:"success_accounts"`
	AbnormalAccounts     int64 `json:"abnormal_accounts"`
	SuspectedDegradation int64 `json:"suspected_degradation"`
}
type IntelligentTestFilter struct {
	Page, PageSize                                                 int
	AccountID, GroupID                                             int64
	Search, Platform, AccountType, AccountStatus, TestType, Status string
	AntiDegradation                                                *bool
	OnlyAbnormal                                                   bool
	From, To                                                       *time.Time
}
type IntelligentTestAccounts struct {
	Items    []IntelligentTestAccountCard `json:"items"`
	Total    int64                        `json:"total"`
	Page     int                          `json:"page"`
	PageSize int                          `json:"page_size"`
	Overview IntelligentTestOverview      `json:"overview"`
}
type IntelligentTestRecords struct {
	Items    []*IntelligentTestRecord `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}
type IntelligentTestEnqueue struct {
	AccountIDs []int64  `json:"account_ids"`
	TestTypes  []string `json:"test_types"`
	// Models optionally overrides the configured model per test type for this
	// run. Empty or missing entries use the saved test setting.
	Models         map[string]string `json:"models,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
}
type IntelligentTestEnqueued struct {
	CreatedCount int                      `json:"created_count"`
	ReusedCount  int                      `json:"reused_count"`
	Records      []*IntelligentTestRecord `json:"records"`
	Reused       bool                     `json:"reused"`
}

// Public types intentionally cannot carry notes, credentials, raw outputs, prompts or errors.
type AccountCapability struct {
	AccountID   int64               `json:"account_id"`
	Platform    string              `json:"platform"`
	AccountType string              `json:"account_type"`
	Tests       []PublicAccountTest `json:"tests"`
}
type PublicAccountTest struct {
	Evaluation  map[string]any `json:"evaluation,omitempty"`
	ID          int64          `json:"id"`
	AccountID   int64          `json:"account_id"`
	TestType    string         `json:"test_type"`
	Status      string         `json:"status"`
	Score       *float64       `json:"score"`
	Result      string         `json:"result"`
	ResultImage string         `json:"result_image"`
	DurationMS  int64          `json:"duration_ms"`
	Model       string         `json:"model"`
	CreatedAt   time.Time      `json:"created_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
}
type PublicAccountTests struct {
	Items    []PublicAccountTest `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}
type IntelligentTestRepository interface {
	IsAdmin(context.Context, int64) (bool, error)
	Settings(context.Context) ([]IntelligentTestSetting, error)
	UpdateSetting(context.Context, int64, *IntelligentTestSetting) error
	Enqueue(context.Context, int64, IntelligentTestEnqueue) (*IntelligentTestEnqueued, error)
	Accounts(context.Context, IntelligentTestFilter) (*IntelligentTestAccounts, error)
	Records(context.Context, IntelligentTestFilter) (*IntelligentTestRecords, error)
	Get(context.Context, int64) (*IntelligentTestRecord, error)
	Claim(context.Context) (*IntelligentTestRecord, error)
	Finish(context.Context, *IntelligentTestRecord) error
	DeferForCapacity(context.Context, *IntelligentTestRecord) error
	Cancel(context.Context, int64, int64) (*IntelligentTestRecord, error)
	Reevaluate(context.Context, int64, int64, func(*IntelligentTestRecord) error) (*IntelligentTestRecord, error)
	Capabilities(context.Context, int64, IntelligentTestFilter) ([]AccountCapability, int64, error)
	PublicRecords(context.Context, int64, IntelligentTestFilter) (*PublicAccountTests, error)
	PublicGet(context.Context, int64, int64) (*PublicAccountTest, error)
}
type IntelligentTestRunner interface {
	RunIntelligentTest(context.Context, *IntelligentTestRecord) error
}
