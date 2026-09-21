package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"go.uber.org/zap"
)

// 动态代理打票(uDeal 模式):供应商 iplist API 每次返回一个短命 socks5h 端口,
// 实测端口「空闲约 60 秒即失效、持续使用则长期存活且出口 IP 不变」。
// 因此打票(采集 292/332)必须走动态端口;命中后把该端口绑定到账号:
//   - 采集:绑定有效期内所有探测都走绑定端口(重打票 = 同一出口 IP);
//   - 业务:绑定有效期内账号出站代理被替换为绑定端口(出口 IP 与打票一致);
//   - 保活:后台循环定期经绑定端口打轻量请求,端口在无业务流量时也不失效;
//   - 失效:保活发现端口死亡 → 清绑定 → 下个采集周期用新端口重绑。
//
// 绑定只存账号 extra(jsonb,走 UpdateExtra 原子 merge),不动 proxy_id / proxies 表,
// 功能开关关闭或删除代码后零残留。

const (
	// SettingKeyDynamicProxyEnabled 动态代理打票总开关("true"/"false")。默认关。
	SettingKeyDynamicProxyEnabled = "openai_dynamic_proxy_enabled"
	// SettingKeyDynamicProxyIplistURL iplist 提取接口完整 URL(含 custom/password/num 等参数模板)。
	SettingKeyDynamicProxyIplistURL = "openai_dynamic_proxy_iplist_url"
	// SettingKeyDynamicProxyUser socks5h 认证用户名(如 userId-7030-custom-19247)。
	SettingKeyDynamicProxyUser = "openai_dynamic_proxy_user"
	// SettingKeyDynamicProxyPass socks5h 认证密码。
	SettingKeyDynamicProxyPass = "openai_dynamic_proxy_pass"

	// AccountExtraDynamicProxyBinding 账号 extra 中的动态代理绑定键。
	AccountExtraDynamicProxyBinding = "dynamic_proxy_binding"

	// dynProxyBindTTL 绑定生命周期上限。端口靠保活续命,但为防供应商侧
	// 长会话异常,到期强制换新端口重绑(出口 IP 会变,属预期)。
	dynProxyBindTTL = 120 * time.Minute
	// dynProxyKeepaliveInterval 保活间隔。实测空闲 ~60s 失效,30s 留一倍余量。
	dynProxyKeepaliveInterval = 30 * time.Second
	// dynProxyExtractBatch 每次向 iplist 提取的端口数(取第一个可用的)。
	dynProxyExtractBatch = 3
)

// DynamicProxyEndpoint iplist 接口返回的一个端口。
type DynamicProxyEndpoint struct {
	IP       string `json:"ip"`
	Port     string `json:"port"`
	OriginIP string `json:"origin_ip"`
}

// DynamicProxyBinding 账号 extra 中保存的绑定快照。
type DynamicProxyBinding struct {
	IP        string    `json:"ip"`
	Port      string    `json:"port"`
	OriginIP  string    `json:"origin_ip"`
	User      string    `json:"user"`
	Pass      string    `json:"pass"`
	BoundAt   time.Time `json:"bound_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// LastKeepalive 最近一次保活成功时间,供管理端展示端口存活状态。
	LastKeepalive time.Time `json:"last_keepalive"`
}

// Alive 绑定是否仍在生命周期内。端口是否真活由保活循环维护。
func (b *DynamicProxyBinding) Alive(now time.Time) bool {
	return b != nil && now.Before(b.ExpiresAt)
}

// URL 绑定的 socks5h 出站地址。
func (b *DynamicProxyBinding) URL() string {
	if b == nil {
		return ""
	}
	u := &url.URL{
		Scheme: "socks5h",
		Host:   b.IP + ":" + b.Port,
	}
	if b.User != "" && b.Pass != "" {
		u.User = url.UserPassword(b.User, b.Pass)
	}
	return u.String()
}

// SyntheticProxy 把绑定转成 Proxy 对象,注入 account.Proxy,
// 让全部出站路径(66 处 account.Proxy.URL())自动走绑定端口。
func (b *DynamicProxyBinding) SyntheticProxy() *Proxy {
	if b == nil {
		return nil
	}
	expires := b.ExpiresAt
	return &Proxy{
		ID:        0, // 合成代理不对应 proxies 表行
		Name:      "dyn-binding",
		Protocol:  "socks5h",
		Host:      b.IP,
		Port:      portToInt(b.Port),
		Username:  b.User,
		Password:  b.Pass,
		Status:    StatusActive,
		ExpiresAt: &expires,
	}
}

func portToInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ParseDynamicProxyBinding 从账号 extra 读绑定;缺失/损坏/过期返回 nil。
func ParseDynamicProxyBinding(extra map[string]any, now time.Time) *DynamicProxyBinding {
	raw, ok := extra[AccountExtraDynamicProxyBinding]
	if !ok || raw == nil {
		return nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var b DynamicProxyBinding
	if err := json.Unmarshal(buf, &b); err != nil {
		return nil
	}
	if b.IP == "" || b.Port == "" {
		return nil
	}
	if !b.Alive(now) {
		return nil
	}
	return &b
}

// ApplyDynamicProxyOverride 在账号加载后调用:开关开启且绑定有效时,
// 用合成代理替换 account.Proxy,业务流量即走绑定端口(出口 IP 与打票一致)。
// 合成代理 ID=0:凡以 Proxy.ID 反查 proxies 表的逻辑(计数/ops 归因/身份校验)
// 视其为"无代理行",不污染统计。
func ApplyDynamicProxyOverride(account *Account, now time.Time) {
	if account == nil || !DynamicProxyRuntimeEnabled() {
		return
	}
	b := ParseDynamicProxyBinding(account.Extra, now)
	if b == nil {
		return
	}
	account.Proxy = b.SyntheticProxy()
}

// dynamicProxyConfig 运行时配置快照。
type dynamicProxyConfig struct {
	enabled  bool
	iplist   string
	user     string
	pass     string
}

// dynProxyRuntimeEnabled 是开关的进程级快照,供 repository 映射器在
// 每行账号加载时零成本读取(不可能每行走 DB)。发布点:构造、采集周期、
// 保活周期、管理端设置写入。零值=false=现状行为,回滚安全。
var dynProxyRuntimeEnabled atomic.Bool

// SetDynamicProxyRuntimeEnabled 发布动态代理总开关快照。
func SetDynamicProxyRuntimeEnabled(on bool) { dynProxyRuntimeEnabled.Store(on) }

// DynamicProxyRuntimeEnabled 读开关快照;repository 映射器用。
func DynamicProxyRuntimeEnabled() bool { return dynProxyRuntimeEnabled.Load() }

func (s *OpenAIGatewayService) dynamicProxyEnabled(ctx context.Context) bool {
	on := false
	if s != nil && s.settingService != nil {
		v, err := s.settingService.GetSettingValue(ctx, SettingKeyDynamicProxyEnabled)
		if err == nil && strings.TrimSpace(v) != "" {
			on = v == "true" || v == "1"
		}
	}
	SetDynamicProxyRuntimeEnabled(on)
	return on
}

func (s *OpenAIGatewayService) dynamicProxyConfig(ctx context.Context) (dynamicProxyConfig, bool) {
	if s == nil || s.settingService == nil {
		return dynamicProxyConfig{}, false
	}
	cfg := dynamicProxyConfig{enabled: s.dynamicProxyEnabled(ctx)}
	if !cfg.enabled {
		return cfg, false
	}
	var err error
	if cfg.iplist, err = s.settingService.GetSettingValue(ctx, SettingKeyDynamicProxyIplistURL); err != nil {
		return cfg, false
	}
	cfg.user, _ = s.settingService.GetSettingValue(ctx, SettingKeyDynamicProxyUser)
	cfg.pass, _ = s.settingService.GetSettingValue(ctx, SettingKeyDynamicProxyPass)
	if strings.TrimSpace(cfg.iplist) == "" {
		return cfg, false
	}
	return cfg, true
}

// ExtractDynamicProxyEndpoints 调 iplist 接口取 n 个端口。
func ExtractDynamicProxyEndpoints(ctx context.Context, httpClient *http.Client, iplistURL string, n int) ([]DynamicProxyEndpoint, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	u := iplistURL
	if n > 1 {
		// 模板里 num=1 时替换为 n;否则追加。
		if strings.Contains(u, "num=") {
			u = replaceQueryParam(u, "num", fmt.Sprintf("%d", n))
		} else {
			u = u + "&num=" + fmt.Sprintf("%d", n)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		return nil, fmt.Errorf("iplist http %d: %s", resp.StatusCode, snippet)
	}
	var payload struct {
		Code int                    `json:"code"`
		Msg  string                 `json:"msg"`
		Data []DynamicProxyEndpoint `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		return nil, fmt.Errorf("iplist parse: %w (body: %s)", err, snippet)
	}
	if payload.Code != 200 || len(payload.Data) == 0 {
		return nil, fmt.Errorf("iplist empty: code=%d msg=%s", payload.Code, payload.Msg)
	}
	return payload.Data, nil
}

func replaceQueryParam(raw, key, val string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return u.String()
}

// acquireDynamicProxy 提取一批端口并挑第一个立刻可用的(实测偶发单端口提取即死)。
func (s *OpenAIGatewayService) acquireDynamicProxy(ctx context.Context, cfg dynamicProxyConfig) (*DynamicProxyEndpoint, error) {
	eps, err := ExtractDynamicProxyEndpoints(ctx, s.plainHTTPClient(), cfg.iplist, dynProxyExtractBatch)
	if err != nil {
		return nil, err
	}
	// 随机起点避免总是压在第一个端口上。
	start := rand.IntN(len(eps))
	for i := 0; i < len(eps); i++ {
		ep := eps[(start+i)%len(eps)]
		if ep.IP == "" || ep.Port == "" {
			continue
		}
		if s.endpointAlive(ctx, &ep, cfg) {
			return &ep, nil
		}
	}
	return nil, errors.New("iplist returned no live endpoint")
}

// endpointAlive 新端口立刻打一发 api.ipify.org,通=可用。
func (s *OpenAIGatewayService) endpointAlive(ctx context.Context, ep *DynamicProxyEndpoint, cfg dynamicProxyConfig) bool {
	b := &DynamicProxyBinding{IP: ep.IP, Port: ep.Port, User: cfg.user, Pass: cfg.pass}
	proxyURL := b.URL()
	if proxyURL == "" || s.httpUpstream == nil {
		return false
	}
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return false
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, 0, 1)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}

// plainHTTPClient 提取接口用的普通 client(不走任何代理)。
func (s *OpenAIGatewayService) plainHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// bindDynamicProxy 把端口写进账号 extra(原子 merge,不覆盖其它 extra 键)。
func (s *OpenAIGatewayService) bindDynamicProxy(ctx context.Context, account *Account, ep *DynamicProxyEndpoint, cfg dynamicProxyConfig) error {
	now := time.Now()
	b := &DynamicProxyBinding{
		IP:            ep.IP,
		Port:          ep.Port,
		OriginIP:      ep.OriginIP,
		User:          cfg.user,
		Pass:          cfg.pass,
		BoundAt:       now,
		ExpiresAt:     now.Add(dynProxyBindTTL),
		LastKeepalive: now,
	}
	buf, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		return err
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{AccountExtraDynamicProxyBinding: m}); err != nil {
		return err
	}
	account.Extra[AccountExtraDynamicProxyBinding] = m
	logger.L().Info("openai_dynamic_proxy bound",
		zap.Int64("account_id", account.ID),
		zap.String("endpoint", ep.IP+":"+ep.Port),
		zap.String("origin_ip", ep.OriginIP))
	return nil
}

// unbindDynamicProxy 清绑定(保活发现端口死亡 / 管理端手动解绑 / 开关关闭)。
func (s *OpenAIGatewayService) unbindDynamicProxy(ctx context.Context, accountID int64) error {
	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{AccountExtraDynamicProxyBinding: nil}); err != nil {
		return err
	}
	logger.L().Info("openai_dynamic_proxy unbound", zap.Int64("account_id", accountID))
	return nil
}

// refreshBindingKeepalive 记录一次保活成功(只写时间戳,merge 单个键)。
func (s *OpenAIGatewayService) refreshBindingKeepalive(ctx context.Context, accountID int64, b *DynamicProxyBinding, now time.Time) {
	b.LastKeepalive = now
	buf, err := json.Marshal(b)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		return
	}
	_ = s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{AccountExtraDynamicProxyBinding: m})
}

// StartDynamicProxyKeepalive 启动绑定保活循环(随网关服务生命周期)。
func (s *OpenAIGatewayService) StartDynamicProxyKeepalive() {
	if s == nil {
		return
	}
	s.dynProxyKeepaliveOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.dynProxyKeepaliveCancel = cancel
		go s.dynamicProxyKeepaliveLoop(ctx)
	})
}

// StopDynamicProxyKeepalive 停保活循环(测试/关闭用)。
func (s *OpenAIGatewayService) StopDynamicProxyKeepalive() {
	if s == nil || s.dynProxyKeepaliveCancel == nil {
		return
	}
	s.dynProxyKeepaliveCancel()
}

func (s *OpenAIGatewayService) dynamicProxyKeepaliveLoop(ctx context.Context) {
	ticker := time.NewTicker(dynProxyKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.dynamicProxyKeepaliveTick(ctx)
		}
	}
}

// dynamicProxyKeepaliveTick 对每个有活绑定的账号经绑定端口打一发轻量请求。
// 成功 → 刷新 last_keepalive;失败 → 端口已死,清绑定,下轮采集用新端口重绑。
func (s *OpenAIGatewayService) dynamicProxyKeepaliveTick(ctx context.Context) {
	if s == nil || s.accountRepo == nil || !s.dynamicProxyEnabled(ctx) {
		return
	}
	accounts, err := s.accountRepo.ListAllByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return
	}
	now := time.Now()
	for i := range accounts {
		account := accounts[i]
		if account.Status != StatusActive {
			continue
		}
		b := ParseDynamicProxyBinding(account.Extra, now)
		if b == nil {
			continue
		}
		// 最近刚用过/刚保活过的跳过,避免对同一端口打太密。
		if now.Sub(b.LastKeepalive) < dynProxyKeepaliveInterval/2 {
			continue
		}
		ok := s.probeDynamicProxyAlive(ctx, b)
		if ok {
			s.refreshBindingKeepalive(ctx, account.ID, b, now)
			continue
		}
		logger.L().Warn("openai_dynamic_proxy keepalive failed, unbinding",
			zap.Int64("account_id", account.ID),
			zap.String("endpoint", b.IP+":"+b.Port))
		_ = s.unbindDynamicProxy(ctx, account.ID)
	}
}

// probeDynamicProxyAlive 经绑定端口 GET 一个轻量地址,通=端口活着。
func (s *OpenAIGatewayService) probeDynamicProxyAlive(ctx context.Context, b *DynamicProxyBinding) bool {
	proxyURL := b.URL()
	if proxyURL == "" || s.httpUpstream == nil {
		return false
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return false
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, 0, 1)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}


// DynamicProxyStatusView 管理端展示:配置 + 全部活绑定。
type DynamicProxyStatusView struct {
	Enabled    bool                       `json:"enabled"`
	IplistURL  string                     `json:"iplist_url"`
	User       string                     `json:"user"`
	PassSet    bool                       `json:"pass_set"`
	Bindings   []DynamicProxyBindingView  `json:"bindings"`
}

// DynamicProxyBindingView 单个账号的绑定快照。
type DynamicProxyBindingView struct {
	AccountID     int64     `json:"account_id"`
	AccountName   string    `json:"account_name"`
	IP            string    `json:"ip"`
	Port          string    `json:"port"`
	OriginIP      string    `json:"origin_ip"`
	BoundAt       time.Time `json:"bound_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	LastKeepalive time.Time `json:"last_keepalive"`
}

// DynamicProxyStatus 读配置 + 扫 OpenAI 账号的活绑定。
func (s *OpenAIGatewayService) DynamicProxyStatus(ctx context.Context) (*DynamicProxyStatusView, error) {
	view := &DynamicProxyStatusView{}
	cfg, on := s.dynamicProxyConfig(ctx)
	view.Enabled = on
	if on {
		view.IplistURL = MaskIplistURL(cfg.iplist)
		view.User = cfg.user
		view.PassSet = cfg.pass != ""
	}
	if s.accountRepo == nil {
		return view, nil
	}
	accounts, err := s.accountRepo.ListAllByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return view, err
	}
	for i := range accounts {
		account := accounts[i]
		b := ParseDynamicProxyBindingRaw(account.Extra)
		if b == nil {
			continue
		}
		view.Bindings = append(view.Bindings, DynamicProxyBindingView{
			AccountID:     account.ID,
			AccountName:   account.Name,
			IP:            b.IP,
			Port:          b.Port,
			OriginIP:      b.OriginIP,
			BoundAt:       b.BoundAt,
			ExpiresAt:     b.ExpiresAt,
			LastKeepalive: b.LastKeepalive,
		})
	}
	return view, nil
}

// ParseDynamicProxyBindingRaw 读绑定但不判过期(管理端展示用)。
func ParseDynamicProxyBindingRaw(extra map[string]any) *DynamicProxyBinding {
	raw, ok := extra[AccountExtraDynamicProxyBinding]
	if !ok || raw == nil {
		return nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var b DynamicProxyBinding
	if err := json.Unmarshal(buf, &b); err != nil {
		return nil
	}
	if b.IP == "" || b.Port == "" {
		return nil
	}
	return &b
}

// SetDynamicProxyConfig 写动态代理配置;pass 传空=保留原值(掩码回显不回写)。
func (s *OpenAIGatewayService) SetDynamicProxyConfig(ctx context.Context, enabled bool, iplist, user, pass string) error {
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return errors.New("setting repository unavailable")
	}
	updates := map[string]string{
		SettingKeyDynamicProxyEnabled: fmt.Sprintf("%t", enabled),
	}
	if iplist != "" {
		if IsMaskedProxyURL(iplist) {
			return errors.New("iplist url looks masked; send the real url")
		}
		if HasMaskedIplistQuery(iplist) {
			stored, _ := s.settingService.GetSettingValue(ctx, SettingKeyDynamicProxyIplistURL)
			iplist = mergeMaskedIplistURL(iplist, stored)
		}
		updates[SettingKeyDynamicProxyIplistURL] = iplist
	}
	if user != "" {
		updates[SettingKeyDynamicProxyUser] = user
	}
	if pass != "" && !IsMaskedProxyURL(pass) {
		updates[SettingKeyDynamicProxyPass] = pass
	}
	if err := s.settingService.settingRepo.SetMultiple(ctx, updates); err != nil {
		return err
	}
	SetDynamicProxyRuntimeEnabled(enabled)
	return nil
}

// RebindDynamicProxy 管理端手动重绑:立刻提取新端口并绑定(替换旧绑定)。
func (s *OpenAIGatewayService) RebindDynamicProxy(ctx context.Context, accountID int64) (*DynamicProxyBinding, error) {
	cfg, on := s.dynamicProxyConfig(ctx)
	if !on {
		return nil, infraerrors.Conflict("DYNAMIC_PROXY_DISABLED", "dynamic proxy is disabled; enable it before rebinding")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ep, aerr := s.acquireDynamicProxy(ctx, cfg)
	if aerr != nil {
		return nil, aerr
	}
	if berr := s.bindDynamicProxy(ctx, account, ep, cfg); berr != nil {
		return nil, berr
	}
	return ParseDynamicProxyBinding(account.Extra, time.Now()), nil
}

// UnbindDynamicProxyAccount 管理端解绑。
func (s *OpenAIGatewayService) UnbindDynamicProxyAccount(ctx context.Context, accountID int64) error {
	return s.unbindDynamicProxy(ctx, accountID)
}

// dynProxySecretQueryKeys iplist 接口的敏感 query 参数名(custom=商家 key、password=供应商密码)。
var dynProxySecretQueryKeys = map[string]bool{
	"password": true, "passwd": true, "pwd": true,
	"key": true, "apikey": true, "token": true, "secret": true,
	"custom": true,
}

const dynProxyMaskPlaceholder = "***"

// MaskIplistURL 掩码 iplist URL 的敏感 query 参数,其余部分原样返回。
func MaskIplistURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	q := parsed.Query()
	changed := false
	for k := range q {
		if dynProxySecretQueryKeys[strings.ToLower(k)] && q.Get(k) != "" && q.Get(k) != dynProxyMaskPlaceholder {
			q.Set(k, dynProxyMaskPlaceholder)
			changed = true
		}
	}
	if !changed {
		return parsed.String()
	}
	parsed.RawQuery = strings.ReplaceAll(q.Encode(), url.QueryEscape(dynProxyMaskPlaceholder), dynProxyMaskPlaceholder)
	return parsed.String()
}

// HasMaskedIplistQuery 判断 iplist URL 是否还带掩码占位的敏感参数。
func HasMaskedIplistQuery(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	q := parsed.Query()
	for k := range q {
		if dynProxySecretQueryKeys[strings.ToLower(k)] && q.Get(k) == dynProxyMaskPlaceholder {
			return true
		}
	}
	return false
}

// mergeMaskedIplistURL 把掩码占位的敏感参数从库里现值还原;非掩码部分以前端传的为准。
func mergeMaskedIplistURL(incoming, stored string) string {
	if !HasMaskedIplistQuery(incoming) || strings.TrimSpace(stored) == "" {
		return incoming
	}
	inParsed, err := url.Parse(incoming)
	if err != nil {
		return incoming
	}
	stParsed, err := url.Parse(stored)
	if err != nil {
		return incoming
	}
	inQ := inParsed.Query()
	stQ := stParsed.Query()
	// 单值化:同名参数以最后一次出现为准(前端回显 URL 可能被追加过参数)
	single := url.Values{}
	for k, vs := range inQ {
		if len(vs) > 0 {
			single.Set(k, vs[len(vs)-1])
		}
	}
	for k := range single {
		if dynProxySecretQueryKeys[strings.ToLower(k)] && single.Get(k) == dynProxyMaskPlaceholder {
			if sv := stQ.Get(k); sv != "" {
				single.Set(k, sv)
			}
		}
	}
	inParsed.RawQuery = single.Encode()
	return inParsed.String()
}
