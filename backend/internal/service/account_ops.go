package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const accountOpsSettingsKey = "account_ops_notifications_v1"

type AccountOpsConfig struct {
	Enabled         bool   `json:"enabled"`
	Recipient       string `json:"recipient"`
	BalanceLow      bool   `json:"balance_low"`
	WeeklyQuota     bool   `json:"weekly_quota"`
	CooldownMinutes int    `json:"cooldown_minutes"`
}

func defaultAccountOpsConfig() AccountOpsConfig {
	return AccountOpsConfig{BalanceLow: true, WeeklyQuota: true, CooldownMinutes: 60}
}
func ValidateAccountOpsConfig(c AccountOpsConfig) error {
	if c.CooldownMinutes < 5 || c.CooldownMinutes > 1440 {
		return errors.New("cooldown_minutes must be between 5 and 1440")
	}
	if c.Recipient != "" {
		address, err := mail.ParseAddress(c.Recipient)
		if err != nil || address.Address != c.Recipient || strings.ContainsAny(c.Recipient, "\r\n") || len(c.Recipient) > 254 {
			return errors.New("recipient must be one email address")
		}
	}
	if c.Enabled && (c.Recipient == "" || (!c.BalanceLow && !c.WeeklyQuota)) {
		return errors.New("select an alert type and configure a recipient before enabling")
	}
	return nil
}
func (c AccountOpsConfig) Allows(kind string) bool {
	return c.Enabled && c.Recipient != "" && ((kind == "balance_low" && c.BalanceLow) || (kind == "weekly_quota" && c.WeeklyQuota))
}

type AccountOpsEvent struct {
	AccountID   int64      `json:"account_id"`
	AccountName string     `json:"account_name"`
	Kind        string     `json:"kind"`
	Signal      string     `json:"signal"`
	HTTPStatus  int        `json:"http_status"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	Occurrences int64      `json:"occurrences"`
	State       string     `json:"state"`
	LastSentAt  *time.Time `json:"last_sent_at"`
	NextSendAt  time.Time  `json:"next_send_at"`
	Attempts    int        `json:"attempts"`
	Lease       string     `json:"-"`
}
type AccountOpsRepository interface {
	Record(context.Context, AccountOpsEvent) error
	Claim(context.Context) (*AccountOpsEvent, error)
	Complete(context.Context, *AccountOpsEvent, string, time.Duration) error
	SuppressDisabled(context.Context, AccountOpsConfig) error
	List(context.Context, int, int) ([]AccountOpsEvent, error)
}
type accountOpsEmailSender interface {
	SendEmail(context.Context, string, string, string) error
}

type AccountOpsService struct {
	settings   SettingRepository
	repo       AccountOpsRepository
	email      accountOpsEmailSender
	config     atomic.Value
	queue      chan AccountOpsEvent
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	lifecycle  sync.Mutex
	settingsMu sync.Mutex
	dropped    atomic.Uint64
	failures   atomic.Uint64
}

func NewAccountOpsService(settings SettingRepository, repo AccountOpsRepository, email accountOpsEmailSender) *AccountOpsService {
	s := &AccountOpsService{settings: settings, repo: repo, email: email, queue: make(chan AccountOpsEvent, 256)}
	s.config.Store(defaultAccountOpsConfig())
	return s
}
func (s *AccountOpsService) GetConfig(ctx context.Context) (AccountOpsConfig, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	c := defaultAccountOpsConfig()
	raw, err := s.settings.GetValue(ctx, accountOpsSettingsKey)
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	}
	if err != nil {
		return c, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return c, err
		}
	}
	if err = ValidateAccountOpsConfig(c); err != nil {
		return c, err
	}
	s.config.Store(c)
	return c, nil
}
func (s *AccountOpsService) SaveConfig(ctx context.Context, c AccountOpsConfig) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	c.Recipient = strings.TrimSpace(c.Recipient)
	if err := ValidateAccountOpsConfig(c); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = s.settings.Set(ctx, accountOpsSettingsKey, string(raw)); err != nil {
		return err
	}
	s.config.Store(c)
	return s.repo.SuppressDisabled(ctx, c)
}
func (s *AccountOpsService) Observe(account *Account, status int, headers http.Header, body []byte) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	c := s.currentConfig()
	if !c.Enabled {
		return
	}
	kind, signal := classifyAccountOpsFailure(account.Platform, status, headers, body)
	if !c.Allows(kind) {
		return
	}
	name := []rune(account.Name)
	if len(name) > 120 {
		name = name[:120]
	}
	event := AccountOpsEvent{AccountID: account.ID, AccountName: string(name), Kind: kind, Signal: signal, HTTPStatus: status}
	// The hot request path never waits for SMTP or the database. Raw errors and credentials do not enter the queue.
	select {
	case s.queue <- event:
	default:
		s.dropped.Add(1)
	}
}
func (s *AccountOpsService) Start() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.refreshConfig(ctx)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-s.queue:
				if !s.currentConfig().Allows(event.Kind) {
					continue
				}
				query, stop := context.WithTimeout(ctx, 3*time.Second)
				err := s.repo.Record(query, event)
				stop()
				if err != nil {
					s.failures.Add(1)
				}
			case <-ticker.C:
				if s.refreshConfig(ctx) {
					s.deliver(ctx)
				}
			}
		}
	}()
}
func (s *AccountOpsService) Stop() {
	s.lifecycle.Lock()
	cancel := s.cancel
	s.lifecycle.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
	}
}
func (s *AccountOpsService) refreshConfig(ctx context.Context) bool {
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := s.GetConfig(query)
	if err != nil {
		s.config.Store(defaultAccountOpsConfig())
		s.failures.Add(1)
		return false
	}
	if err = s.repo.SuppressDisabled(query, c); err != nil {
		s.failures.Add(1)
		return false
	}
	return c.Enabled
}
func (s *AccountOpsService) deliver(ctx context.Context) {
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		query, cancel := context.WithTimeout(ctx, 5*time.Second)
		event, err := s.repo.Claim(query)
		cancel()
		if err != nil {
			s.failures.Add(1)
			return
		}
		if event == nil {
			return
		}
		s.deliverEvent(ctx, event)
	}
}
func (s *AccountOpsService) deliverEvent(ctx context.Context, event *AccountOpsEvent) {
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	c, err := s.GetConfig(query)
	cancel()
	state := "suppressed"
	delay := time.Hour
	if err == nil {
		delay = time.Duration(c.CooldownMinutes) * time.Minute
	}
	if err != nil {
		state = "failed"
	} else if c.Allows(event.Kind) {
		label := "上游余额不足"
		if event.Kind == "weekly_quota" {
			label = "上游周额度已用尽"
		}
		subject := "Sub2API 账号运维：" + label
		body := fmt.Sprintf("<h2>%s</h2><p>账号：%s（#%d）</p><p>上游返回 HTTP %d，匹配信号：%s。</p><p>最近触发：%s；累计匹配 %d 次。</p><p>请在账号管理中检查该账号的上游账单或周额度。此提醒基于失败响应，不代表已查询到准确余额，也不会自动修改账号。</p>", label, html.EscapeString(event.AccountName), event.AccountID, event.HTTPStatus, html.EscapeString(accountOpsSignalLabel(event.Signal)), event.LastSeen.UTC().Format(time.RFC3339), event.Occurrences)
		send, stop := context.WithTimeout(ctx, 30*time.Second)
		if s.email == nil {
			err = errors.New("email unavailable")
		} else {
			err = s.email.SendEmail(send, c.Recipient, subject, body)
		}
		stop()
		state = "sent"
		if err != nil {
			state = "failed"
		}
	}
	if state == "failed" && event.Attempts < 3 {
		delay = 5 * time.Minute
	}
	finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err = s.repo.Complete(finish, event, state, delay); err != nil {
		s.failures.Add(1)
	}
}
func (s *AccountOpsService) List(ctx context.Context, offset, limit int) ([]AccountOpsEvent, error) {
	return s.repo.List(ctx, offset, limit)
}
func (s *AccountOpsService) RuntimeCounters() (uint64, uint64) {
	return s.dropped.Load(), s.failures.Load()
}

func (s *AccountOpsService) currentConfig() AccountOpsConfig {
	c, _ := s.config.Load().(AccountOpsConfig)
	return c
}

type accountOpsObservedKey struct{}

func (s *RateLimitService) observeAccountOps(ctx context.Context, account *Account, status int, headers http.Header, body []byte) context.Context {
	if s == nil || s.accountOps == nil || ctx.Value(accountOpsObservedKey{}) != nil {
		return ctx
	}
	s.accountOps.Observe(account, status, headers, body)
	return context.WithValue(ctx, accountOpsObservedKey{}, true)
}

func accountOpsSignalLabel(signal string) string {
	labels := map[string]string{"balance_error_code": "明确的余额错误码", "balance_error_message": "明确的余额不足提示", "weekly_error_code": "明确的周额度错误码", "weekly_error_message": "明确的周限提示", "codex_weekly_header": "Codex 七天用量窗口已耗尽", "anthropic_weekly_header": "Anthropic 七天用量窗口已耗尽"}
	if label, ok := labels[signal]; ok {
		return label
	}
	return "已识别的上游失败信号"
}
