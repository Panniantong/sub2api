package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const cookieHostMonitorHistoryKey = "cookie_host_monitor_history"

type CookieHostMonitorConfig struct {
	Enabled            bool      `json:"enabled"`
	AccountID          int64     `json:"account_id"`
	Email              string    `json:"email"`
	Host               string    `json:"host"`
	Model              string    `json:"model"`
	InitialWaitSeconds int       `json:"initial_wait_seconds"`
	IntervalSeconds    int       `json:"interval_seconds"`
	ExperimentID       string    `json:"experiment_id"`
	StartedAt          time.Time `json:"started_at"`
}

type CookieHostMonitorSample struct {
	ID              string     `json:"id"`
	ExperimentID    string     `json:"experiment_id"`
	Email           string     `json:"email"`
	AccountID       int64      `json:"account_id"`
	Host            string     `json:"host"`
	Model           string     `json:"model"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      time.Time  `json:"finished_at"`
	Result          string     `json:"result"`
	HTTPStatus      int        `json:"http_status"`
	Response        string     `json:"response"`
	CookieExpiresAt *time.Time `json:"cookie_expires_at,omitempty"`
}

// The same lock serializes manual/background probes and config changes. It is
// separate from Cookie acquisition/rotation locks, so probes cannot stall them.
var cookieHostMonitorMu sync.Mutex

func CookieHostMonitorRunning() bool {
	if !cookieHostMonitorMu.TryLock() {
		return true
	}
	cookieHostMonitorMu.Unlock()
	return false
}

// Rows are newest first. Only definitive no/yes observations delimit an
// episode; transport failures and missing Cookies never imply recovery.
func CookieHostMonitorRecovery(rows []CookieHostMonitorSample, experiment string) (firstNo, lastNo, recovered *time.Time) {
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if r.ExperimentID != experiment {
			continue
		}
		if r.Result == "no" {
			if recovered != nil {
				firstNo, lastNo, recovered = nil, nil, nil
			}
			if firstNo == nil {
				t := r.FinishedAt
				firstNo = &t
			}
			t := r.FinishedAt
			lastNo = &t
		} else if r.Result == "yes" && firstNo != nil && recovered == nil {
			t := r.FinishedAt
			recovered = &t
		}
	}
	return
}

func CookieHostMonitorNext(c CookieHostMonitorConfig, rows []CookieHostMonitorSample) time.Time {
	next := c.StartedAt.Add(time.Duration(c.InitialWaitSeconds) * time.Second)
	for _, row := range rows {
		if row.ExperimentID == c.ExperimentID {
			after := row.FinishedAt.Add(time.Duration(c.IntervalSeconds) * time.Second)
			if after.After(next) {
				next = after
			}
			break
		}
	}
	return next
}

func cookieMonitorEmail(a *Account) string {
	if a == nil {
		return ""
	}
	for _, v := range []string{a.GetCredential("email"), a.GetExtraString("email"), a.Name} {
		v = strings.ToLower(strings.TrimSpace(v))
		if strings.Contains(v, "@") {
			return v
		}
	}
	return ""
}

func cookieHostMonitorOwns(a *Account, settings *OpenAICookieSettings) bool {
	return isOpenAICodexTicketAccount(a) && settings != nil && settings.HostMonitor != nil && settings.HostMonitor.Enabled && ((settings.HostMonitor.AccountID > 0 && a.ID == settings.HostMonitor.AccountID) || (settings.HostMonitor.Email != "" && cookieMonitorEmail(a) == settings.HostMonitor.Email))
}

// CookieHostMonitorOwns also protects administrative and bulk Host edits.
func CookieHostMonitorOwns(a *Account, settings *OpenAICookieSettings) bool {
	return cookieHostMonitorOwns(a, settings)
}

func (s *OpenAIGatewayService) checkCookieHostMonitorLock(ctx context.Context, a *Account) error {
	if s == nil || s.settingService == nil {
		return nil
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return err
	}
	if cookieHostMonitorOwns(a, settings) {
		return fmt.Errorf("账号正在固定 Host 监控，请先关闭监控后再修改 Cookie Host")
	}
	return nil
}

func (s *OpenAIGatewayService) SetCookieHostMonitor(ctx context.Context, c CookieHostMonitorConfig) error {
	if !cookieHostMonitorMu.TryLock() {
		return fmt.Errorf("监控请求执行中，请稍后保存")
	}
	defer cookieHostMonitorMu.Unlock()
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	var selected *Account
	if c.Enabled && c.AccountID > 0 {
		var err error
		selected, err = s.accountRepo.GetByID(ctx, c.AccountID)
		if err != nil {
			return err
		}
		if !isOpenAICodexTicketAccount(selected) || selected.Status != StatusActive {
			return fmt.Errorf("请选择启用的 OpenAI OAuth 账号")
		}
		c.Email = cookieMonitorEmail(selected)
	}
	c.Host = normalizeOpenAICookieHost(c.Host)
	c.Model = strings.TrimSpace(c.Model)
	if c.Model == "" {
		c.Model = "gpt-6-astra"
	}
	if c.Enabled && (!strings.Contains(c.Email, "@") || c.Host == "" || strings.ContainsAny(c.Host, "/ :")) {
		return fmt.Errorf("请填写邮箱和完整 Host 名称")
	}
	if c.IntervalSeconds < 30 || c.IntervalSeconds > 86400 || c.InitialWaitSeconds < 0 || c.InitialWaitSeconds > 604800 || len(c.Model) > 128 {
		return fmt.Errorf("首次等待需为 0–604800 秒，复测间隔需为 30–86400 秒")
	}
	defaults, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		return err
	}
	cookieSettingsMu.Lock()
	defer cookieSettingsMu.Unlock()
	// Re-read under the settings lock to preserve concurrent unrelated saves.
	raw, err := s.settingService.settingRepo.GetValue(ctx, cookieSettingsKey)
	if err != nil {
		return err
	}
	settings := *defaults
	if err = json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	old := settings.HostMonitor
	if old != nil && old.Enabled && c.Enabled && (old.Email != c.Email || old.Host != c.Host || old.AccountID != c.AccountID) {
		return fmt.Errorf("监控已锁定账号和 Host，请先关闭监控再更换")
	}
	if old != nil && old.Enabled && c.Enabled && old.Email == c.Email && old.Host == c.Host && old.Model == c.Model {
		c.ExperimentID, c.StartedAt = old.ExperimentID, old.StartedAt
	} else {
		c.ExperimentID, c.StartedAt = uuid.NewString(), time.Now()
	}
	settings.HostMonitor = &c
	if err := s.settingService.setOpenAICookieSettings(ctx, &settings); err != nil {
		return err
	}
	if selected != nil {
		// Publish isolation first; even a failed binding must never permit scheduling.
		if err := s.pinCookieHostMonitor(ctx, selected, c); err != nil {
			return fmt.Errorf("监控已隔离账号，固定 Host 写入失败：%w", err)
		}
	}
	return nil
}

func (s *OpenAIGatewayService) pinCookieHostMonitor(ctx context.Context, a *Account, c CookieHostMonitorConfig) error {
	return s.accountRepo.UpdateExtra(ctx, a.ID, map[string]any{
		openAICodexCookieHostExtraKey:          c.Host,
		"codex_cookie_host_binding_expires_at": nil,
		"codex_cookie_host_rotation_at":        nil,
		openAICodexCookieRotationNextExtraKey:  nil,
	})
}

func (s *SettingService) CookieHostMonitorSamples(ctx context.Context) ([]CookieHostMonitorSample, error) {
	raw, err := s.settingRepo.GetValue(ctx, cookieHostMonitorHistoryKey)
	if errors.Is(err, ErrSettingNotFound) {
		return []CookieHostMonitorSample{}, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []CookieHostMonitorSample
	if strings.TrimSpace(raw) == "" {
		return []CookieHostMonitorSample{}, nil
	}
	err = json.Unmarshal([]byte(raw), &rows)
	return rows, err
}

func (s *OpenAIGatewayService) runCookieHostMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		_ = s.StartCookieHostMonitorProbe(ctx, false)
	}
}

// Start returns immediately; response streaming/reading and persistence belong
// to the backend and survive a browser closing. Scheduled runs respect first
// wait, while an explicit manual run can establish the initial no baseline.
func (s *OpenAIGatewayService) StartCookieHostMonitorProbe(ctx context.Context, manual bool) error {
	if !cookieHostMonitorMu.TryLock() {
		if manual {
			return fmt.Errorf("监控正在执行")
		}
		return nil
	}
	settings, err := s.settingService.GetOpenAICookieSettings(ctx)
	if err != nil {
		cookieHostMonitorMu.Unlock()
		return err
	}
	c := settings.HostMonitor
	if c == nil || !c.Enabled {
		cookieHostMonitorMu.Unlock()
		if manual {
			return fmt.Errorf("请先启用并保存监控")
		}
		return nil
	}
	rows, err := s.settingService.CookieHostMonitorSamples(ctx)
	if err != nil {
		cookieHostMonitorMu.Unlock()
		return err
	}
	next := CookieHostMonitorNext(*c, rows)
	if !manual && time.Now().Before(next) {
		cookieHostMonitorMu.Unlock()
		return nil
	}
	config := *c
	go func() {
		defer cookieHostMonitorMu.Unlock()
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 110*time.Second)
		defer cancel()
		row := s.probeCookieHostMonitor(runCtx, config)
		rows = append([]CookieHostMonitorSample{row}, rows...)
		if len(rows) > 2000 {
			rows = rows[:2000]
		}
		data, marshalErr := json.Marshal(rows)
		if marshalErr == nil {
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer saveCancel()
			if saveErr := s.settingService.Set(saveCtx, cookieHostMonitorHistoryKey, string(data)); saveErr != nil {
				slog.Warn("cookie host monitor save failed", "error", saveErr)
			}
		}
	}()
	return nil
}

func (s *OpenAIGatewayService) probeCookieHostMonitor(ctx context.Context, c CookieHostMonitorConfig) (row CookieHostMonitorSample) {
	row = CookieHostMonitorSample{ID: uuid.NewString(), ExperimentID: c.ExperimentID, Email: c.Email, Host: c.Host, Model: c.Model, StartedAt: time.Now(), Result: "error"}
	defer func() { row.FinishedAt = time.Now() }()
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		row.Response = "读取账号失败"
		return
	}
	var account *Account
	// Prefer the explicitly selected account; fall back to email after reimport.
	for i := range accounts {
		if accounts[i].ID == c.AccountID && accounts[i].Status == StatusActive && isOpenAICodexTicketAccount(&accounts[i]) {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		for i := range accounts {
			if isOpenAICodexTicketAccount(&accounts[i]) && cookieMonitorEmail(&accounts[i]) == c.Email && accounts[i].Status == StatusActive {
				if account != nil {
					row.Response = "同邮箱有多个启用账号，请只保留一个启用账号"
					return
				}
				account = &accounts[i]
			}
		}
	}
	if account == nil {
		row.Response = "未找到该邮箱的启用账号（重新导入相同邮箱会自动关联）"
		return
	}
	row.AccountID = account.ID
	if err := s.pinCookieHostMonitor(ctx, account, c); err != nil {
		row.Response = "固定 Host 绑定失败：" + err.Error()
		return
	}
	entry, err := s.settingService.LookupOpenAICodexCookie(ctx, c.Host)
	if err != nil || entry == nil {
		row.Result = "cookie_unavailable"
		row.Response = "指定 Host Cookie 缺失或已过期，等待 Cookie 库补齐；未切换到其他 Host"
		return
	}
	row.CookieExpiresAt = &entry.ExpiresAt
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || token == "" {
		row.Response = "获取账号访问令牌失败"
		return
	}
	candidate := *account
	candidate.Extra = make(map[string]any, len(account.Extra)+1)
	for k, v := range account.Extra {
		candidate.Extra[k] = v
	}
	candidate.Extra[openAICodexCookieHostExtraKey] = c.Host
	candidate.openaiCookieValidationCookie = entry.Cookie
	for attempt := 0; attempt < 3; attempt++ {
		row.Result, row.Response, row.HTTPStatus = s.validateOpenAICodexCookieHost(ctx, &candidate, token, entry.Cookie, "", c.Model)
		if row.Result == "yes" || row.Result == "no" || ctx.Err() != nil {
			break
		}
	}
	row.Response = truncateOpenAICodexValidationResponse(row.Response)
	return
}
