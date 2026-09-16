package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewProxyExitInfoProber(cfg *config.Config) service.ProxyExitInfoProber {
	insecure := false
	allowPrivate := false
	validateResolvedIP := true
	maxResponseBytes := defaultProxyProbeResponseMaxBytes
	if cfg != nil {
		insecure = cfg.Security.ProxyProbe.InsecureSkipVerify
		allowPrivate = cfg.Security.URLAllowlist.AllowPrivateHosts
		validateResolvedIP = cfg.Security.URLAllowlist.Enabled
		if cfg.Gateway.ProxyProbeResponseReadMaxBytes > 0 {
			maxResponseBytes = cfg.Gateway.ProxyProbeResponseReadMaxBytes
		}
	}
	if insecure {
		log.Printf("[ProxyProbe] Warning: insecure_skip_verify is not allowed and will cause probe failure.")
	}
	// 构建探测 URL 列表：配置存在时覆盖内置默认列表。
	var configuredTargets []configuredProbeTarget
	if cfg != nil && len(cfg.Security.ProxyProbe.URLs) > 0 {
		configuredTargets = make([]configuredProbeTarget, 0, len(cfg.Security.ProxyProbe.URLs))
		for _, u := range cfg.Security.ProxyProbe.URLs {
			configuredTargets = append(configuredTargets, configuredProbeTarget{
				url:    u.URL,
				parser: u.Parser,
			})
		}
	}

	return &proxyProbeService{
		insecureSkipVerify:  insecure,
		allowPrivateHosts:   allowPrivate,
		validateResolvedIP:  validateResolvedIP,
		maxResponseBytes:    maxResponseBytes,
		configuredProbeURLs: configuredTargets,
	}
}

const (
	defaultProxyProbeTimeout          = 15 * time.Second // 放宽至15s以兼容慢速代理（如geo.miyaip 8s延迟）
	defaultProxyProbeResponseMaxBytes = int64(1024 * 1024)
)

// probeURLs 按优先级排列的内置探测 URL 列表。
// 某些 AI API 专用代理只允许访问特定域名，因此需要多个备选。
var probeURLs = []struct {
	url    string
	parser string
}{
	{"http://ip-api.com/json/?lang=zh-CN", "ip-api"},
	{"http://api64.ipify.org?format=json", "ipify"},
}

type configuredProbeTarget struct {
	url    string
	parser string
}

type proxyProbeService struct {
	insecureSkipVerify  bool
	allowPrivateHosts   bool
	validateResolvedIP  bool
	maxResponseBytes    int64
	configuredProbeURLs []configuredProbeTarget
}

func (s *proxyProbeService) ProbeProxy(ctx context.Context, proxyURL string) (*service.ProxyExitInfo, int64, error) {
	// 兼容多格式输入，规范化后再探测
	normalized := strings.TrimSpace(proxyURL)
	if _, _, err := proxyurl.Parse(normalized); err != nil {
		if t, p, ferr := proxyurl.ParseFlexible(normalized, "http"); ferr == nil && p != nil {
			normalized = t
		}
	}
	candidates := buildProxyCandidates(normalized)
	var lastErr error
	for idx, cand := range candidates {
		client, err := httpclient.GetClient(httpclient.Options{
			ProxyURL:           cand,
			Timeout:            defaultProxyProbeTimeout,
			InsecureSkipVerify: s.insecureSkipVerify,
			ValidateResolvedIP: s.validateResolvedIP,
			AllowPrivateHosts:  s.allowPrivateHosts,
		})
		if err != nil {
			// 客户端构造失败只与配置有关（不支持的协议/非法参数），与候选
			// 协议无关，重试其他候选无意义，直接失败。
			lastErr = fmt.Errorf("failed to create proxy client for %s: %w", urlRedacted(cand), err)
			break
		}
		var probeErr error
		if len(s.configuredProbeURLs) > 0 {
			for _, probe := range s.configuredProbeURLs {
				exitInfo, latencyMs, err := s.probeWithURL(ctx, client, probe.url, probe.parser)
				if err == nil {
					return exitInfo, latencyMs, nil
				}
				probeErr = err
			}
		} else {
			for _, probe := range probeURLs {
				exitInfo, latencyMs, err := s.probeWithURL(ctx, client, probe.url, probe.parser)
				if err == nil {
					return exitInfo, latencyMs, nil
				}
				probeErr = err
			}
		}
		lastErr = probeErr
		// 仅当原始协议为连接层失败时才尝试回退协议；业务层失败（503、JSON）说明代理已连通，无需换协议
		if idx == 0 {
			if !isProxyConnectError(lastErr) {
				break
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no proxy candidates")
	}
	return nil, 0, fmt.Errorf("all probe URLs failed, last error: %w", lastErr)
}

// buildProxyCandidates 基于原始代理URL生成多协议候选，顺序：原始优先，然后 http/socks5h/https 互备
func buildProxyCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{""}
	}
	_, parsed, err := proxyurl.Parse(raw)
	if err != nil {
		// 若严格解析失败，尝试柔性解析后重建
		if t, p, ferr := proxyurl.ParseFlexible(raw, "http"); ferr == nil && p != nil {
			parsed = p
			raw = t
		} else {
			return []string{raw}
		}
	}
	origScheme := strings.ToLower(parsed.Scheme)
	// 首候选使用归一化形式（Parse 已将 socks5 升级为 socks5h，确保远端 DNS 解析）。
	// 直接用 raw 会导致 socks5:// 输入首试弱语义（客户端本地解析 DNS）。
	raw = parsed.String()
	// 已去重
	seen := map[string]bool{raw: true}
	cands := []string{raw}
	// 定义回退顺序：http<->socks5h, https<->socks5h, socks5h->http
	var alts []string
	switch origScheme {
	case "http":
		alts = []string{"socks5h", "https"}
	case "https":
		alts = []string{"http", "socks5h"}
	case "socks5", "socks5h":
		alts = []string{"http", "https"}
	default:
		alts = []string{"http", "socks5h"}
	}
	for _, alt := range alts {
		dup := *parsed
		dup.Scheme = alt
		s := dup.String()
		if !seen[s] {
			seen[s] = true
			cands = append(cands, s)
		}
	}
	return cands
}

func urlRedacted(raw string) string {
	if u, err := url.Parse(raw); err == nil && u != nil {
		return u.Redacted()
	}
	if len(raw) > 12 {
		return raw[:3] + "***" + raw[len(raw)-3:]
	}
	return "***"
}

func isProxyConnectError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 传输层统一包装为 "proxy connection failed: ..."（probeWithURL），直接判定。
	if strings.HasPrefix(msg, "proxy connection failed") {
		return true
	}
	// 业务层失败（状态码/解析）即使正文恰好含有连接关键字也不换协议。
	if strings.HasPrefix(msg, "request failed with status") ||
		strings.HasPrefix(msg, "failed to parse") ||
		strings.HasPrefix(msg, "failed to read response") ||
		strings.Contains(msg, "ip-api request failed") ||
		strings.Contains(msg, "ipify") ||
		strings.Contains(msg, "chatgpt-trace") {
		return false
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "proxyconnect") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "tls:") ||
		strings.Contains(lower, "certificate") ||
		strings.Contains(lower, "eof")
}

func (s *proxyProbeService) probeWithURL(ctx context.Context, client *http.Client, url string, parser string) (*service.ProxyExitInfo, int64, error) {
	startTime := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("proxy connection failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	latencyMs := time.Since(startTime).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		return nil, latencyMs, fmt.Errorf("request failed with status: %d", resp.StatusCode)
	}

	maxResponseBytes := s.maxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultProxyProbeResponseMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, latencyMs, fmt.Errorf("failed to read response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, latencyMs, fmt.Errorf("proxy probe response exceeds limit: %d", maxResponseBytes)
	}

	switch parser {
	case "ip-api":
		return s.parseIPAPI(body, latencyMs)
	case "ipify":
		return s.parseIPify(body, latencyMs)
	case "chatgpt-trace":
		return s.parseChatGPTTrace(body, latencyMs)
	default:
		return nil, latencyMs, fmt.Errorf("unknown parser: %s", parser)
	}
}

func (s *proxyProbeService) parseIPAPI(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	var ipInfo struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Query       string `json:"query"`
		City        string `json:"city"`
		Region      string `json:"region"`
		RegionName  string `json:"regionName"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
	}

	if err := json.Unmarshal(body, &ipInfo); err != nil {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, latencyMs, fmt.Errorf("failed to parse response: %w (body: %s)", err, preview)
	}
	if strings.ToLower(ipInfo.Status) != "success" {
		if ipInfo.Message == "" {
			ipInfo.Message = "ip-api request failed"
		}
		return nil, latencyMs, fmt.Errorf("ip-api request failed: %s", ipInfo.Message)
	}

	region := ipInfo.RegionName
	if region == "" {
		region = ipInfo.Region
	}
	return &service.ProxyExitInfo{
		IP:          ipInfo.Query,
		City:        ipInfo.City,
		Region:      region,
		Country:     ipInfo.Country,
		CountryCode: ipInfo.CountryCode,
	}, latencyMs, nil
}

func (s *proxyProbeService) parseIPify(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	var result struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, latencyMs, fmt.Errorf("failed to parse ipify response: %w", err)
	}
	if result.IP == "" {
		return nil, latencyMs, fmt.Errorf("ipify: no IP found in response")
	}
	return &service.ProxyExitInfo{
		IP: result.IP,
	}, latencyMs, nil
}

// parseChatGPTTrace 解析 Cloudflare trace 端点（如 chatgpt.com/cdn-cgi/trace）的纯文本响应。
// 响应按行给出键值对，其中 ip= 为出口 IP，loc= 为国家代码。
func (s *proxyProbeService) parseChatGPTTrace(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	var ip, loc string
	for _, line := range strings.Split(string(body), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "ip":
			ip = strings.TrimSpace(value)
		case "loc":
			loc = strings.TrimSpace(value)
		}
	}
	if ip == "" {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, latencyMs, fmt.Errorf("chatgpt-trace: no ip= found in response (body: %s)", preview)
	}
	info := &service.ProxyExitInfo{
		IP: ip,
	}
	if loc != "" {
		info.CountryCode = loc
	}
	return info, latencyMs, nil
}
