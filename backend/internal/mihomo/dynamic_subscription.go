package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxDynamicProxies = 256

// dynamicProxy is kept private so credentials cannot accidentally escape in a
// status DTO or an error value.
type dynamicProxy struct {
	Scheme   string
	Host     string
	Port     int
	Username string
	Password string
}

func normalizeDynamicProxies(raw []string) ([]string, error) {
	if len(raw) > maxDynamicProxies {
		return nil, fmt.Errorf("at most %d dynamic proxies", maxDynamicProxies)
	}
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, normalized, err := parseDynamicProxy(value)
		if err != nil {
			return nil, fmt.Errorf("dynamic proxy line %d: %w", index+1, err)
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func parseDynamicProxy(raw string) (dynamicProxy, string, error) {
	raw = strings.TrimSpace(raw)
	scheme := "http"
	if prefix, rest, ok := strings.Cut(raw, "://"); ok {
		scheme, raw = strings.ToLower(prefix), rest
	}
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return dynamicProxy{}, "", errors.New("dynamic proxy must use http, https, socks5, or socks5h")
	}
	var candidates []dynamicProxy
	add := func(endpoint, credentials string, escaped bool) {
		host, portText, err := net.SplitHostPort(endpoint)
		if err != nil || host == "" || strings.ContainsAny(host, " /?#@%\\\t\r\n") {
			return
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return
		}
		username, password, ok := strings.Cut(credentials, ":")
		if escaped {
			username, err = url.PathUnescape(username)
			if err != nil {
				return
			}
			password, err = url.PathUnescape(password)
			if err != nil {
				return
			}
		}
		if !ok || username == "" || password == "" || strings.ContainsAny(username+password, "\r\n\x00") {
			return
		}
		candidates = append(candidates, dynamicProxy{scheme, strings.ToLower(host), port, username, password})
	}
	// Colon exports are raw credentials; URL-style exports use percent encoding.
	// Try colon formats first so a literal @ in an exported password survives.
	for i, char := range raw {
		if char == ':' {
			add(raw[:i], raw[i+1:], false)
			add(raw[i+1:], raw[:i], false)
		}
	}
	if len(candidates) == 0 {
		if i := strings.LastIndex(raw, "@"); i >= 0 {
			add(raw[i+1:], raw[:i], true)
		}
		if i := strings.Index(raw, "@"); i >= 0 {
			add(raw[:i], raw[i+1:], true)
		}
	}
	if len(candidates) == 0 {
		return dynamicProxy{}, "", errors.New("invalid proxy format; use host:port:user:password or user:password@host:port")
	}
	if len(candidates) > 1 {
		return dynamicProxy{}, "", errors.New("ambiguous proxy format; use user:password@host:port with URL-encoded credentials")
	}
	proxy := candidates[0]
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port)), User: url.UserPassword(proxy.Username, proxy.Password)}
	return proxy, u.String(), nil
}

func dynamicProxyNodes(raw []string) ([]map[string]any, map[string]string, error) {
	normalized, err := normalizeDynamicProxies(raw)
	if err != nil {
		return nil, nil, err
	}
	nodes := make([]map[string]any, 0, len(normalized))
	names := make(map[string]string, len(normalized))
	for index, value := range normalized {
		proxy, _, err := parseDynamicProxy(value)
		if err != nil {
			return nil, nil, err
		}
		hash := sha256.Sum256([]byte(value))
		name := "DYNAMIC-" + hex.EncodeToString(hash[:])[:16]
		nodeType := proxy.Scheme
		switch nodeType {
		case "https":
			nodeType = "http"
		case "socks5h":
			// Mihomo's socks5 outbound performs remote DNS for proxy hosts;
			// its YAML type is still "socks5".
			nodeType = "socks5"
		}
		node := map[string]any{
			"name":     name,
			"type":     nodeType,
			"server":   proxy.Host,
			"port":     proxy.Port,
			"username": proxy.Username,
			"password": proxy.Password,
		}
		if proxy.Scheme == "https" {
			node["tls"] = true
		}
		nodes = append(nodes, node)
		names[name] = fmt.Sprintf("dynamic-%02d", index+1)
	}
	return nodes, names, nil
}

// buildDynamicClashSubscription produces a provider-compatible YAML document.
// The manager consumes the node representation directly, while this function
// keeps the conversion reusable for diagnostics and future provider endpoints.
func buildDynamicClashSubscription(raw []string) ([]byte, error) {
	nodes, _, err := dynamicProxyNodes(raw)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(map[string]any{"proxies": nodes})
}
