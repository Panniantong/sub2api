package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const intelligenceMonitorSettingKey = "openai_intelligence_monitor"

type intelligenceMonitorPrompt struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Enabled bool   `json:"enabled"`
}

type intelligenceMonitorConfig struct {
	Enabled        bool                        `json:"enabled"`
	IntervalSecond int                         `json:"interval_seconds"`
	MaxRounds      int                         `json:"max_rounds"`
	ModelID        string                      `json:"model_id"`
	GroupIDs       []int64                     `json:"group_ids"`
	Prompts        []intelligenceMonitorPrompt `json:"prompts"`
	LastRunAt      string                      `json:"last_run_at,omitempty"`
}

type intelligenceMonitorAccountResult struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	Case        string `json:"case"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	CookieHost  string `json:"cookie_host,omitempty"`
	Output      string `json:"output,omitempty"`
	HTML        string `json:"html,omitempty"`
	Error       string `json:"error,omitempty"`
}

type intelligenceMonitorRuntime struct {
	Running     bool   `json:"running"`
	RoundID     string `json:"round_id,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
	Total       int    `json:"total"`
	Completed   int    `json:"completed"`
	AccountID   int64  `json:"account_id,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	PromptID    string `json:"prompt_id,omitempty"`
	PromptTitle string `json:"prompt_title,omitempty"`
	Message     string `json:"message,omitempty"`
}

type intelligenceMonitorRound struct {
	ID         string                             `json:"id"`
	StartedAt  string                             `json:"started_at"`
	FinishedAt string                             `json:"finished_at,omitempty"`
	Results    []intelligenceMonitorAccountResult `json:"results"`
}

type intelligenceMonitorStore struct {
	Config  intelligenceMonitorConfig  `json:"config"`
	Rounds  []intelligenceMonitorRound `json:"rounds"`
	Runtime intelligenceMonitorRuntime `json:"runtime"`
}

var intelligenceMonitorDefaults = []intelligenceMonitorPrompt{
	{ID: "html", Title: "SVG 鹈鹕骑车", Prompt: "创建个新的html，用svg画一个鹈鹕骑车的动图，不要进行任何测试。", Enabled: true},
	{ID: "candy", Title: "数糖果", Prompt: "在一个黑色的袋子里放 有三种口味的糖果，每种糖果有两 种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已 知不同口味 的糖果和 不同形状 的数量统计如下表。参赛者需要在活动 前决定摸出 的糖果数目，那 么，最少取出多少个糖果 才能保 证手中同时拥有不同形 状的苹果味和桃子味 的糖果？（同时手中有圆形苹果味匹配五角星桃子 味糖果，或者有 圆形桃 子味匹配五角星 苹果味糖果都满 足 要求） 苹果味 桃子味 西 瓜 味 圆形 7 9 8 五角星形 7 6 4 帮我做一下 不调 用任何工具 也不调用联网", Enabled: true},
	{ID: "host_validation", Title: "Host 降智验证", Prompt: "don't search the internet, do you know Thibault Sottiaux on X. answer yes or no", Enabled: true},
}

var intelligenceMonitorMu sync.Mutex

func defaultIntelligenceMonitorStore() intelligenceMonitorStore {
	return intelligenceMonitorStore{Config: intelligenceMonitorConfig{Enabled: false, IntervalSecond: 3600, MaxRounds: 20, ModelID: "gpt-6-astra", Prompts: append([]intelligenceMonitorPrompt(nil), intelligenceMonitorDefaults...)}}
}

func (h *AccountHandler) loadIntelligenceMonitor(ctx context.Context) intelligenceMonitorStore {
	store := defaultIntelligenceMonitorStore()
	if h.settingService == nil {
		return store
	}
	raw, err := h.settingService.GetValue(ctx, intelligenceMonitorSettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return store
	}
	if json.Unmarshal([]byte(raw), &store) != nil {
		return defaultIntelligenceMonitorStore()
	}
	if store.Config.IntervalSecond < 30 {
		store.Config.IntervalSecond = 3600
	}
	if store.Config.MaxRounds <= 0 {
		store.Config.MaxRounds = 20
	}
	if strings.TrimSpace(store.Config.ModelID) == "" {
		store.Config.ModelID = "gpt-6-astra"
	}
	return store
}

func (h *AccountHandler) saveIntelligenceMonitor(ctx context.Context, store intelligenceMonitorStore) error {
	if h.settingService == nil {
		return fmt.Errorf("setting service unavailable")
	}
	raw, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return h.settingService.Set(ctx, intelligenceMonitorSettingKey, string(raw))
}

func (h *AccountHandler) GetIntelligenceMonitorConfig(c *gin.Context) {
	store := h.loadIntelligenceMonitor(c.Request.Context())
	c.JSON(200, store.Config)
}

func (h *AccountHandler) UpdateIntelligenceMonitorConfig(c *gin.Context) {
	var input intelligenceMonitorConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if input.IntervalSecond < 30 || input.IntervalSecond > 30*24*3600 {
		response.BadRequest(c, "interval_seconds must be between 30 and 2592000")
		return
	}
	if input.MaxRounds <= 0 || input.MaxRounds > 200 {
		input.MaxRounds = 20
	}
	if len(input.Prompts) == 0 {
		input.Prompts = intelligenceMonitorDefaults
	}
	store := h.loadIntelligenceMonitor(c.Request.Context())
	store.Config = input
	if err := h.saveIntelligenceMonitor(c.Request.Context(), store); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(200, input)
}

func (h *AccountHandler) GetIntelligenceMonitorResults(c *gin.Context) {
	store := h.loadIntelligenceMonitor(c.Request.Context())
	limit := 20
	if parsed, err := strconv.Atoi(c.Query("limit")); err == nil && parsed > 0 && parsed <= 200 {
		limit = parsed
	}
	if len(store.Rounds) > limit {
		store.Rounds = store.Rounds[:limit]
	}
	c.JSON(200, store.Rounds)
}

func (h *AccountHandler) GetIntelligenceMonitorRuntime(c *gin.Context) {
	store := h.loadIntelligenceMonitor(c.Request.Context())
	c.JSON(200, store.Runtime)
}

func (h *AccountHandler) RunIntelligenceMonitor(c *gin.Context) {
	go h.runIntelligenceMonitor(context.Background())
	c.JSON(202, gin.H{"message": "intelligence monitor run started"})
}

func (h *AccountHandler) startIntelligenceMonitor() {
	if h == nil || h.settingService == nil || h.accountTestService == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			store := h.loadIntelligenceMonitor(context.Background())
			if !store.Config.Enabled || len(store.Config.Prompts) == 0 {
				continue
			}
			last, _ := time.Parse(time.RFC3339, store.Config.LastRunAt)
			if !last.IsZero() && time.Since(last) < time.Duration(store.Config.IntervalSecond)*time.Second {
				continue
			}
			h.runIntelligenceMonitor(context.Background())
		}
	}()
}

func (h *AccountHandler) runIntelligenceMonitor(ctx context.Context) {
	intelligenceMonitorMu.Lock()
	defer intelligenceMonitorMu.Unlock()
	store := h.loadIntelligenceMonitor(ctx)
	accounts, _, err := h.adminService.ListAccounts(ctx, 1, 10000, string(service.PlatformOpenAI), "", "active", "", 0, "", "id", "asc")
	if err != nil {
		store.Runtime = intelligenceMonitorRuntime{Running: false, FinishedAt: time.Now().UTC().Format(time.RFC3339Nano), Message: "读取执行账号失败: " + err.Error()}
		_ = h.saveIntelligenceMonitor(ctx, store)
		return
	}
	round := intelligenceMonitorRound{ID: fmt.Sprintf("%d", time.Now().UnixNano()), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	selectedAccounts := make([]service.Account, 0, len(accounts))
	enabledPrompts := make([]intelligenceMonitorPrompt, 0, len(store.Config.Prompts))
	for i := range accounts {
		if accounts[i].IsActive() && intelligenceMonitorAccountInGroups(&accounts[i], store.Config.GroupIDs) {
			selectedAccounts = append(selectedAccounts, accounts[i])
		}
	}
	for _, prompt := range store.Config.Prompts {
		if prompt.Enabled && strings.TrimSpace(prompt.Prompt) != "" {
			enabledPrompts = append(enabledPrompts, prompt)
		}
	}
	store.Runtime = intelligenceMonitorRuntime{Running: true, RoundID: round.ID, StartedAt: round.StartedAt, Total: len(selectedAccounts) * len(enabledPrompts), Message: "正在准备本轮检测"}
	_ = h.saveIntelligenceMonitor(ctx, store)
	for _, account := range selectedAccounts {
		for _, prompt := range enabledPrompts {
			store.Runtime.AccountID = account.ID
			store.Runtime.AccountName = account.Name
			store.Runtime.PromptID = prompt.ID
			store.Runtime.PromptTitle = prompt.Title
			store.Runtime.Message = "正在执行检测"
			_ = h.saveIntelligenceMonitor(ctx, store)
			result := intelligenceMonitorAccountResult{AccountID: account.ID, AccountName: account.Name, Case: prompt.ID, Title: prompt.Title, Status: "error"}
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequest("POST", "/admin/accounts/intelligence-test", nil).WithContext(ctx)
			if runErr := h.accountTestService.IntelligenceTestAccount(ginCtx, account.ID, prompt.ID, prompt.Prompt, store.Config.ModelID); runErr != nil {
				result.Error = runErr.Error()
			}
			parseIntelligenceMonitorSSE(recorder.Body.String(), &result)
			round.Results = append(round.Results, result)
			store.Runtime.Completed++
			store.Runtime.Message = "已完成当前检测"
			_ = h.saveIntelligenceMonitor(ctx, store)
		}
	}
	round.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	store.Rounds = append([]intelligenceMonitorRound{round}, store.Rounds...)
	if store.Config.MaxRounds <= 0 {
		store.Config.MaxRounds = 20
	}
	if len(store.Rounds) > store.Config.MaxRounds {
		store.Rounds = store.Rounds[:store.Config.MaxRounds]
	}
	store.Config.LastRunAt = round.FinishedAt
	store.Runtime.Running = false
	store.Runtime.FinishedAt = round.FinishedAt
	store.Runtime.AccountID = 0
	store.Runtime.AccountName = ""
	store.Runtime.PromptID = ""
	store.Runtime.PromptTitle = ""
	store.Runtime.Message = "本轮检测已完成"
	_ = h.saveIntelligenceMonitor(ctx, store)
}

func intelligenceMonitorAccountInGroups(account *service.Account, groupIDs []int64) bool {
	if account == nil || len(groupIDs) == 0 {
		return account != nil
	}
	configured := make(map[int64]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id > 0 {
			configured[id] = struct{}{}
		}
	}
	if len(configured) == 0 {
		return true
	}
	for _, id := range account.GroupIDs {
		if _, ok := configured[id]; ok {
			return true
		}
	}
	for _, group := range account.AccountGroups {
		if _, ok := configured[group.GroupID]; ok {
			return true
		}
	}
	for _, group := range account.Groups {
		if group != nil {
			if _, ok := configured[group.ID]; ok {
				return true
			}
		}
	}
	return false
}

func parseIntelligenceMonitorSSE(body string, result *intelligenceMonitorAccountResult) {
	for scanner := bufio.NewScanner(strings.NewReader(body)); scanner.Scan(); {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event service.TestEvent
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			continue
		}
		if event.CookieHost != "" {
			result.CookieHost = event.CookieHost
		}
		if event.Type == "content" {
			result.Output += event.Text
		}
		if event.Type == "html" {
			result.HTML = event.Text
		}
		if event.Type == "error" {
			result.Error = event.Error
			result.Status = "error"
		}
		if event.Type == "test_complete" {
			if event.Success {
				result.Status = "success"
			}
		}
	}
}
