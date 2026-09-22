package mihomo

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

var nodeNameURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s]+`)

// Display labels are never used as controller targets or state keys.
func sanitizeNodeDisplayName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	name = nodeNameURL.ReplaceAllString(name, "[redacted URL]")
	name = logredact.RedactText(name, "token", "secret", "uuid", "authorization", "subscription")
	runes := []rune(strings.TrimSpace(name))
	if len(runes) > 120 {
		runes = runes[:120]
	}
	return string(runes)
}

// NodeDisplayName resolves managed node IDs without changing their identity.
// Existing installations recover missing labels on the next subscription apply.
func NodeDisplayName(id string) string {
	label := ""
	managers.Range(func(key, _ any) bool {
		m, ok := key.(*Manager)
		if !ok {
			return true
		}
		m.mu.Lock()
		if !m.closed {
			label = m.saved.NodeNames[id]
		}
		m.mu.Unlock()
		return label == ""
	})
	if label == "" {
		label = id
	}
	return sanitizeNodeDisplayName(label)
}
