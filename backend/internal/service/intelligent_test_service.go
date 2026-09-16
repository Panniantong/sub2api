package service

import (
	"context"
	"errors"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ErrIntelligentTestNotFound = infraerrors.NotFound("INTELLIGENT_TEST_NOT_FOUND", "test result not found")
var ErrIntelligentTestForbidden = infraerrors.Forbidden("INTELLIGENT_TEST_FORBIDDEN", "administrator access required")
var ErrIntelligentTestConflict = infraerrors.Conflict("INTELLIGENT_TEST_CONFLICT", "idempotency key was already used with different parameters")

func intelligentTestBad(message string) error {
	return infraerrors.BadRequest("INTELLIGENT_TEST_INVALID", message)
}

type IntelligentTestService struct {
	repo       IntelligentTestRepository
	runner     IntelligentTestRunner
	evaluators map[string]IntelligentTestEvaluator
	cancel     context.CancelFunc
	mu         sync.Mutex
	wg         sync.WaitGroup
}

func NewIntelligentTestService(repo IntelligentTestRepository, runner *AccountTestService) *IntelligentTestService {
	return &IntelligentTestService{repo: repo, runner: runner, evaluators: DefaultIntelligentTestEvaluators()}
}
func (s *IntelligentTestService) authorize(ctx context.Context, actor int64) error {
	ok, err := s.repo.IsAdmin(ctx, actor)
	if err != nil {
		return err
	}
	if !ok {
		return ErrIntelligentTestForbidden
	}
	return nil
}
func normalizeIntelligentFilter(f IntelligentTestFilter) IntelligentTestFilter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 24
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	return f
}
func (s *IntelligentTestService) Accounts(ctx context.Context, actor int64, f IntelligentTestFilter) (*IntelligentTestAccounts, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.repo.Accounts(ctx, normalizeIntelligentFilter(f))
}
func (s *IntelligentTestService) Records(ctx context.Context, actor int64, f IntelligentTestFilter) (*IntelligentTestRecords, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.repo.Records(ctx, normalizeIntelligentFilter(f))
}
func (s *IntelligentTestService) Get(ctx context.Context, actor, id int64) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	record, err := s.repo.Get(ctx, id)
	if err == nil && record.ResultImage == "" && strings.Contains(record.Result, "<svg") {
		record.ResultImage, _, _ = PrepareIntelligentSVGPreview(record.Result)
	}
	return record, err
}
func (s *IntelligentTestService) Settings(ctx context.Context, actor int64) ([]IntelligentTestSetting, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.repo.Settings(ctx)
}
func (s *IntelligentTestService) UpdateSetting(ctx context.Context, actor int64, setting *IntelligentTestSetting) error {
	if err := s.authorize(ctx, actor); err != nil {
		return err
	}
	if setting == nil || !regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`).MatchString(setting.TestType) {
		return intelligentTestBad("invalid test type")
	}
	cfg := &setting.Config
	cfg.Execution = nil // Only a runner may produce an execution snapshot.
	if err := validateIntelligentTestConfig(cfg, s.evaluators); err != nil {
		return err
	}
	return s.repo.UpdateSetting(ctx, actor, setting)
}

func validateIntelligentTestConfig(cfg *IntelligentTestConfig, evaluators map[string]IntelligentTestEvaluator) error {
	if err := validateIntelligentTextModel(cfg.Model); err != nil {
		return intelligentTestBad(err.Error())
	}
	cfg.Prompt = strings.TrimSpace(cfg.Prompt)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if len(cfg.Prompt) == 0 || utf8.RuneCountInString(cfg.Prompt) > 16000 || utf8.RuneCountInString(cfg.Model) > 200 || utf8.RuneCountInString(cfg.ExpectedAnswer) > 200 || utf8.RuneCountInString(cfg.AnswerUnit) > 32 {
		return intelligentTestBad("prompt/model/answer length is invalid")
	}
	if cfg.TimeoutSeconds < 30 || cfg.TimeoutSeconds > 600 {
		return intelligentTestBad("timeout_seconds must be 30–600")
	}
	if _, ok := evaluators[cfg.Evaluator]; !ok {
		return intelligentTestBad("unknown evaluator")
	}
	if cfg.Evaluator == "exact_answer" && strings.TrimSpace(cfg.ExpectedAnswer) == "" {
		return intelligentTestBad("expected_answer is required")
	}
	if cfg.AnswerType != "" && cfg.AnswerType != "auto" && cfg.AnswerType != "number" && cfg.AnswerType != "text" {
		return intelligentTestBad("invalid answer_type")
	}
	if cfg.AnswerFormat != "" && cfg.AnswerFormat != "answer_line" && cfg.AnswerFormat != "free_text" {
		return intelligentTestBad("invalid answer_format")
	}
	if cfg.AnswerUnitMode != "" && cfg.AnswerUnitMode != "none" && cfg.AnswerUnitMode != "configured" && cfg.AnswerUnitMode != "legacy" {
		return intelligentTestBad("invalid answer_unit_mode")
	}
	if cfg.Evaluator == "exact_answer" && cfg.AnswerUnitMode == "configured" && strings.TrimSpace(cfg.AnswerUnit) == "" {
		return intelligentTestBad("请选择不接受单位，或填写允许的单位")
	}
	if cfg.Evaluator == "exact_answer" && cfg.AnswerType == "number" {
		if _, ok := normalizeIntelligentNumber(cfg.ExpectedAnswer, intelligentAnswerUnit(*cfg)); !ok {
			return intelligentTestBad("数值题的标准答案不是有效数值")
		}
	}
	return nil
}
func (s *IntelligentTestService) Enqueue(ctx context.Context, actor int64, req IntelligentTestEnqueue) (*IntelligentTestEnqueued, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`).MatchString(req.IdempotencyKey) {
		return nil, intelligentTestBad("idempotency_key must contain 16–100 letters, digits, underscores or hyphens")
	}
	if len(req.AccountIDs) < 1 || len(req.AccountIDs) > 100 || len(req.TestTypes) < 1 || len(req.TestTypes) > 10 || len(req.AccountIDs)*len(req.TestTypes) > 200 {
		return nil, intelligentTestBad("select 1–100 accounts and at most 200 tests")
	}
	seen := map[int64]bool{}
	for _, id := range req.AccountIDs {
		if id < 1 || seen[id] {
			return nil, intelligentTestBad("account IDs must be positive and unique")
		}
		seen[id] = true
	}
	types := map[string]bool{}
	for _, kind := range req.TestTypes {
		if types[kind] {
			return nil, intelligentTestBad("test types must be unique")
		}
		types[kind] = true
	}
	for kind, model := range req.Models {
		if err := validateIntelligentTextModel(model); err != nil {
			return nil, intelligentTestBad(err.Error())
		}
		if !types[kind] || utf8.RuneCountInString(strings.TrimSpace(model)) > 200 {
			return nil, intelligentTestBad("model overrides must target selected test types and be at most 200 characters")
		}
	}
	return s.repo.Enqueue(ctx, actor, req)
}
func (s *IntelligentTestService) Capabilities(ctx context.Context, user int64, f IntelligentTestFilter) ([]AccountCapability, int64, error) {
	return s.repo.Capabilities(ctx, user, normalizeIntelligentFilter(f))
}
func (s *IntelligentTestService) PublicRecords(ctx context.Context, user int64, f IntelligentTestFilter) (*PublicAccountTests, error) {
	return s.repo.PublicRecords(ctx, user, normalizeIntelligentFilter(f))
}
func (s *IntelligentTestService) PublicGet(ctx context.Context, user, id int64) (*PublicAccountTest, error) {
	record, err := s.repo.PublicGet(ctx, user, id)
	if err == nil && record.ResultImage == "" && strings.Contains(record.Result, "<svg") {
		record.ResultImage, _, _ = PrepareIntelligentSVGPreview(record.Result)
	}
	return record, err
}
func (s *IntelligentTestService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.work(ctx)
				}
			}
		}()
	}
}
func (s *IntelligentTestService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
func (s *IntelligentTestService) work(ctx context.Context) {
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	record, err := s.repo.Claim(claimCtx)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("intelligent test claim failed", "error", err)
		}
		return
	}
	if record == nil {
		return
	}
	s.run(ctx, record)
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer saveCancel()
	if record.Status == "queued" {
		if err := s.repo.DeferForCapacity(saveCtx, record); err != nil {
			slog.Error("intelligent capacity requeue failed", "id", record.ID, "error", err)
		}
		return
	}
	if err := s.repo.Finish(saveCtx, record); err != nil {
		slog.Error("intelligent test result save failed; lease recovery will record interruption", "id", record.ID, "error", err)
	}
}
func (s *IntelligentTestService) run(ctx context.Context, r *IntelligentTestRecord) {
	start := time.Now()
	defer func() {
		r.DurationMS = time.Since(start).Milliseconds()
		if p := recover(); p != nil {
			r.Status = "failed"
			r.ErrorMessage = "test runner failed unexpectedly"
			slog.Error("intelligent test panic", "id", r.ID, "type", fmt.Sprintf("%T", p))
		}
		if r.Status != "completed" && r.Status != "queued" && r.Status != "running" {
			r.Evaluation = newIntelligentAssessment("")
			r.Evaluation["execution_status"], r.Evaluation["answer_verdict"], r.Evaluation["format_verdict"] = r.Status, "not_evaluated", "not_evaluated"
		}
	}()
	if r.ConfigSnapshot == nil {
		r.Status = "failed"
		r.ErrorMessage = "missing test configuration snapshot"
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(r.ConfigSnapshot.TimeoutSeconds)*time.Second)
	defer cancel()
	err := s.runner.RunIntelligentTest(runCtx, r)
	if errors.Is(err, ErrIntelligentAccountBusy) {
		r.Status = "queued"
		r.QueueReason = "等待账号空闲，不占用额外业务并发"
		var wait *TestAdmissionWaitError
		if errors.As(err, &wait) {
			r.AvailableAt = &wait.Until
			r.QueueReason = wait.Reason + "，任务延后执行"
		}
		return
	}
	if runCtx.Err() != nil {
		r.Status = "failed"
		r.ErrorMessage = "测试超时或服务中断；未自动重试以避免重复计费"
		return
	}
	if err != nil {
		if r.Status == "running" || r.Status == "" {
			r.Status = "failed"
		}
		if r.ErrorMessage == "" {
			r.ErrorMessage = err.Error()
		}
		r.Evaluation = newIntelligentAssessment(r.ConfigSnapshot.Evaluator)
		r.Evaluation["execution_status"], r.Evaluation["answer_verdict"], r.Evaluation["format_verdict"] = "failed", "not_evaluated", "not_evaluated"
		return
	}
	evaluator, ok := s.evaluators[r.ConfigSnapshot.Evaluator]
	if !ok {
		r.Status = "failed"
		r.ErrorMessage = "evaluator unavailable"
		return
	}
	evaluated := evaluator.Evaluate(r.Result, *r.ConfigSnapshot)
	r.Status = evaluated.Status
	r.Score = evaluated.Score
	r.ResultImage = evaluated.Image
	r.Evaluation = evaluated.Detail
}

func (s *IntelligentTestService) PreviewEvaluation(ctx context.Context, actor int64, output string, cfg IntelligentTestConfig) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if len(output) > 512<<10 {
		return nil, intelligentTestBad("试判输出不能超过 512 KiB")
	}
	if err := validateIntelligentTestConfig(&cfg, s.evaluators); err != nil {
		return nil, err
	}
	e := s.evaluators[cfg.Evaluator].Evaluate(output, cfg)
	return &IntelligentTestRecord{Status: e.Status, Score: e.Score, Result: output, ResultImage: e.Image, Evaluation: e.Detail}, nil
}

func (s *IntelligentTestService) Cancel(ctx context.Context, actor, id int64) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.repo.Cancel(ctx, actor, id)
}

func (s *IntelligentTestService) Reevaluate(ctx context.Context, actor, id int64) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.repo.Reevaluate(ctx, actor, id, func(r *IntelligentTestRecord) error {
		if r.ConfigSnapshot == nil || r.RawTruncated || strings.TrimSpace(r.Result) == "" {
			return intelligentTestBad("原始答复或配置不完整，无法自动重评")
		}
		if r.Status != "success" && r.Status != "completed" && r.Status != "suspected_degradation" && !(r.Status == "failed" && r.Evaluation["method"] != nil && r.ErrorMessage == "") {
			return intelligentTestBad("只能重评已经完成且具有完整答复的记录")
		}
		evaluator, ok := s.evaluators[r.ConfigSnapshot.Evaluator]
		if !ok {
			return intelligentTestBad("评估器不可用")
		}
		e := evaluator.Evaluate(r.Result, *r.ConfigSnapshot)
		r.Status, r.Score, r.ResultImage, r.Evaluation = e.Status, e.Score, e.Image, e.Detail
		return nil
	})
}
