// Package proxyurl 提供代理 URL 的统一验证（fail-fast，无效代理不回退直连）
//
// 所有需要解析代理 URL 的地方必须通过此包的 Parse 函数。
// 直接使用 url.Parse 处理代理 URL 是被禁止的。
// 这确保了 fail-fast 行为：无效代理配置在创建时立即失败，
// 而不是在运行时静默回退到直连（产生 IP 关联风险）。
package proxyurl

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// allowedSchemes 代理协议白名单
var allowedSchemes = map[string]bool{
	"http":    true,
	"https":   true,
	"socks5":  true,
	"socks5h": true,
}

// Parse 解析并验证代理 URL。
//
// 语义:
//   - 空字符串 → ("", nil, nil)，表示直连
//   - 非空且有效 → (trimmed, *url.URL, nil)
//   - 非空但无效 → ("", nil, error)，fail-fast 不回退
//
// 验证规则:
//   - TrimSpace 后为空视为直连
//   - url.Parse 失败返回 error（不含原始 URL，防凭据泄露）
//   - Host 为空返回 error（用 Redacted() 脱敏）
//   - Scheme 必须为 http/https/socks5/socks5h
//   - socks5:// 自动升级为 socks5h://（确保 DNS 由代理端解析，防止 DNS 泄漏）
func Parse(raw string) (trimmed string, parsed *url.URL, err error) {
	trimmed = strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil, nil
	}

	parsed, err = url.Parse(trimmed)
	if err != nil {
		// 不使用 %w 包装，避免 url.Parse 的底层错误消息泄漏原始 URL（可能含凭据）
		return "", nil, fmt.Errorf("invalid proxy URL: %v", err)
	}

	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", nil, fmt.Errorf("proxy URL missing host: %s", parsed.Redacted())
	}

	scheme := strings.ToLower(parsed.Scheme)
	if !allowedSchemes[scheme] {
		return "", nil, fmt.Errorf("unsupported proxy scheme %q (allowed: http, https, socks5, socks5h)", scheme)
	}

	// 自动升级 socks5 → socks5h，确保 DNS 由代理端解析，防止 DNS 泄漏。
	// Go 的 golang.org/x/net/proxy 对 socks5:// 默认在客户端本地解析 DNS，
	// 仅 socks5h:// 才将域名发送给代理端做远程 DNS 解析。
	if scheme == "socks5" {
		parsed.Scheme = "socks5h"
		trimmed = parsed.String()
	}

	return trimmed, parsed, nil
}

// ParseFlexible 支持多格式代理输入（兼容前端 proxyParser 的所有格式）：
//   - http://user:pass@host:port
//   - https://host:port
//   - socks5://host:port
//   - host:port:user:pass        (geo.miyaip.app:8001:qsddxeswee:otemiuwmowemouu)
//   - user:pass@host:port
//   - user:pass:host:port
//   - host:port                  (无认证)
//   - [ipv6]:port:user:pass 等带括号 IPv6
//
// defaultScheme 为解析无显式 scheme 时的默认协议（http/https/socks5/socks5h），
// 若为空或非法则默认 http。
func ParseFlexible(raw string, defaultScheme string) (trimmed string, parsed *url.URL, err error) {
	trimmed = strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil, nil
	}
	// 显式 scheme 直接走严格校验
	if regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`).MatchString(trimmed) {
		return Parse(trimmed)
	}
	ds := strings.ToLower(strings.TrimSpace(defaultScheme))
	if !allowedSchemes[ds] {
		ds = "http"
	}
	// 依次尝试无 scheme 的多种常见格式
	if p := tryFlexibleParse(trimmed, ds); p != "" {
		return Parse(p)
	}
	return "", nil, fmt.Errorf("invalid proxy format %q (allowed: http, https, socks5, socks5h + host:port:user:pass etc.)", redactedRaw(raw))
}

func tryFlexibleParse(raw string, defaultScheme string) string {
	// 1) user:pass@host:port  (last @)
	if idx := strings.LastIndex(raw, "@"); idx > 0 {
		creds := raw[:idx]
		hostPort := raw[idx+1:]
		if h, p := parseHostPortFlexible(hostPort); h != "" && p != "" {
			if u, pw := splitCreds(creds); u != "" || pw != "" {
				return buildProxyURL(defaultScheme, h, p, u, pw)
			}
		}
	}
	// 2) host:port:user:pass  (port 为第2字段，password 可含冒号)
	if m := regexp.MustCompile(`^(\[[0-9a-fA-F:.]+\]|[^:@\s]+):(\d+):([^:@]*):(.*)$`).FindStringSubmatch(raw); m != nil {
		host := normalizeHostFlexible(m[1])
		port := m[2]
		u := strings.TrimSpace(m[3])
		pw := strings.TrimSpace(m[4])
		if host != "" && validPort(port) && (u != "" || pw != "") && !strings.Contains(u, "@") && !strings.Contains(pw, "@") {
			return buildProxyURL(defaultScheme, host, port, u, pw)
		}
	}
	// 3) user:pass:host:port  (port 为最后字段)
	if m := regexp.MustCompile(`^([^:@]*):([^:@]*):(\[[0-9a-fA-F:.]+\]|[^:@\s]+):(\d+)$`).FindStringSubmatch(raw); m != nil {
		u := strings.TrimSpace(m[1])
		pw := strings.TrimSpace(m[2])
		host := normalizeHostFlexible(m[3])
		port := m[4]
		if host != "" && validPort(port) && (u != "" || pw != "") {
			return buildProxyURL(defaultScheme, host, port, u, pw)
		}
	}
	// 4) host:port 无认证
	if h, p := parseHostPortFlexible(raw); h != "" && p != "" {
		return buildProxyURL(defaultScheme, h, p, "", "")
	}
	return ""
}

func parseHostPortFlexible(s string) (host, port string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "[") {
		ci := strings.Index(s, "]")
		if ci < 0 || ci+1 >= len(s) || s[ci+1] != ':' {
			return "", ""
		}
		host = normalizeHostFlexible(s[:ci+1])
		port = strings.TrimSpace(s[ci+2:])
		if !validPort(port) {
			return "", ""
		}
		return host, port
	}
	li := strings.LastIndex(s, ":")
	if li <= 0 {
		return "", ""
	}
	host = normalizeHostFlexible(s[:li])
	port = strings.TrimSpace(s[li+1:])
	if host == "" || !validPort(port) {
		return "", ""
	}
	return host, port
}

func normalizeHostFlexible(v string) string {
	h := strings.TrimSpace(v)
	if h == "" || strings.Contains(h, " ") || strings.Contains(h, "@") {
		return ""
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		inner := h[1 : len(h)-1]
		if inner == "" || strings.Contains(inner, " ") {
			return ""
		}
		// 允许 IPv6 字符
		if !regexp.MustCompile(`^[0-9a-fA-F:.]+$`).MatchString(inner) {
			return ""
		}
		return inner
	}
	if strings.Contains(h, ":") {
		return ""
	}
	return h
}

func validPort(p string) bool {
	if p == "" {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	// 1-65535
	n := 0
	for _, c := range p {
		n = n*10 + int(c-'0')
		if n > 65535 {
			return false
		}
	}
	return n >= 1 && n <= 65535
}

func splitCreds(s string) (user, pass string) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		u := strings.TrimSpace(s)
		if u == "" || strings.Contains(u, "@") {
			return "", ""
		}
		return u, ""
	}
	u := strings.TrimSpace(s[:idx])
	pw := strings.TrimSpace(s[idx+1:])
	if !strings.Contains(u, "@") && !strings.Contains(pw, "@") && (u != "" || pw != "") {
		return u, pw
	}
	return "", ""
}

func buildProxyURL(scheme, host, port, user, pass string) string {
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	switch {
	case user != "" && pass != "":
		u.User = url.UserPassword(user, pass)
	case user != "":
		u.User = url.User(user)
	case pass != "":
		u.User = url.UserPassword("", pass)
	}
	return u.String()
}

func redactedRaw(raw string) string {
	// 脱敏：只保留长度和前后字符
	r := strings.TrimSpace(raw)
	if len(r) <= 8 {
		return "***"
	}
	return r[:3] + "***" + r[len(r)-3:]
}
