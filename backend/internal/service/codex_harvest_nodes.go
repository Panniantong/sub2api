package service

import (
	"context"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
)

type CodexHarvestNodeScope struct {
	PoolID    string `json:"pool_id"`
	AccountID int64  `json:"account_id"`
	Identity  string `json:"-"`
	Model     string `json:"model"`
	Blocks    int    `json:"blocks"`
}

type CodexHarvestNodeRecord struct {
	CodexHarvestNodeScope
	ID                  int64      `json:"id"`
	NodeID              string     `json:"node_id"`
	NodeName            string     `json:"node_name"`
	Provider            string     `json:"provider"`
	Successes           int64      `json:"successes"`
	Misses              int64      `json:"misses"`
	NetworkErrors       int64      `json:"network_errors"`
	AccountErrors       int64      `json:"account_errors"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastSuccess         *time.Time `json:"last_success"`
	CooldownUntil       *time.Time `json:"cooldown_until"`
	LatencyMS           int64      `json:"latency_ms"`
	LastResult          string     `json:"last_result"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type CodexHarvestNodePage struct {
	Items []CodexHarvestNodeRecord `json:"items"`
	Total int64                    `json:"total"`
}

type CodexHarvestNodeFeedback struct {
	Scope           CodexHarvestNodeScope
	Node            mihomo.HarvestNode
	Generation      int64
	Result          string
	LatencyMS       int64
	CooldownSeconds int
}

type CodexHarvestNodeRepository interface {
	Snapshot(context.Context, CodexHarvestNodeScope) (int64, []CodexHarvestNodeRecord, error)
	Record(context.Context, CodexHarvestNodeFeedback) (bool, error)
	List(context.Context, int, int) (CodexHarvestNodePage, error)
	Reset(context.Context, int64) error
}

// Ranking is scoped to an account identity, model and ticket shape. Kernel IDs
// are intentionally excluded from persistent ranking because they are random.
func rankCodexHarvestNodes(nodes []mihomo.HarvestNode, records []CodexHarvestNodeRecord, tried map[string]bool, cursor uint64, now time.Time) []mihomo.HarvestNode {
	stats := make(map[string]CodexHarvestNodeRecord, len(records))
	for _, r := range records {
		stats[r.NodeID] = r
	}
	eligible := make([]mihomo.HarvestNode, 0, len(nodes))
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for i := range nodes {
		n := nodes[(i+int(cursor%uint64(len(nodes))))%len(nodes)]
		r := stats[n.ID]
		if tried[n.ID] || r.CooldownUntil != nil && r.CooldownUntil.After(now) {
			continue
		}
		eligible = append(eligible, n)
	}
	recent := func(r CodexHarvestNodeRecord) bool {
		return r.LastSuccess != nil && r.LastSuccess.After(now.Add(-7*24*time.Hour))
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := stats[eligible[i].ID], stats[eligible[j].ID]
		if recent(a) != recent(b) {
			return recent(a)
		}
		if !recent(a) {
			return false
		}
		if a.ConsecutiveFailures != b.ConsecutiveFailures {
			return a.ConsecutiveFailures < b.ConsecutiveFailures
		}
		ar := float64(a.Successes) / float64(a.Successes+a.Misses+a.NetworkErrors+1)
		br := float64(b.Successes) / float64(b.Successes+b.Misses+b.NetworkErrors+1)
		if ar != br {
			return ar > br
		}
		if !a.LastSuccess.Equal(*b.LastSuccess) {
			return a.LastSuccess.After(*b.LastSuccess)
		}
		return a.LatencyMS < b.LatencyMS
	})
	return eligible
}
