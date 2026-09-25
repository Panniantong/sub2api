package service

import "sync/atomic"

// OpenAICyberSessionIdentityMetricsSnapshot is a process-local, non-sensitive
// coverage snapshot. It intentionally records only resolution classes, never
// raw identity values, API keys, users, IPs, or request bodies.
type OpenAICyberSessionIdentityMetricsSnapshot struct {
	Total          uint64
	Resolved       uint64
	Missing        uint64
	Conflict       uint64
	Invalid        uint64
	Inherited      uint64
	StrictRejected uint64
	WSIdentitySwap uint64
}

var openAICyberSessionIdentityMetrics struct {
	total          atomic.Uint64
	resolved       atomic.Uint64
	missing        atomic.Uint64
	conflict       atomic.Uint64
	invalid        atomic.Uint64
	inherited      atomic.Uint64
	strictRejected atomic.Uint64
	wsIdentitySwap atomic.Uint64
}

// ObserveOpenAICyberSessionIdentity records one gateway admission decision.
// Callers must invoke it once per HTTP request or WebSocket turn.
func ObserveOpenAICyberSessionIdentity(metadata OpenAIClientSessionIdentityMetadata, inherited, strictRejected, wsIdentitySwap bool) {
	openAICyberSessionIdentityMetrics.total.Add(1)
	switch metadata.Status {
	case OpenAIClientSessionIdentityResolved:
		openAICyberSessionIdentityMetrics.resolved.Add(1)
	case OpenAIClientSessionIdentityMissing:
		openAICyberSessionIdentityMetrics.missing.Add(1)
	case OpenAIClientSessionIdentityConflict:
		openAICyberSessionIdentityMetrics.conflict.Add(1)
	case OpenAIClientSessionIdentityInvalid:
		openAICyberSessionIdentityMetrics.invalid.Add(1)
	}
	if inherited {
		openAICyberSessionIdentityMetrics.inherited.Add(1)
	}
	if strictRejected {
		openAICyberSessionIdentityMetrics.strictRejected.Add(1)
	}
	if wsIdentitySwap {
		openAICyberSessionIdentityMetrics.wsIdentitySwap.Add(1)
	}
}

func SnapshotOpenAICyberSessionIdentityMetrics() OpenAICyberSessionIdentityMetricsSnapshot {
	return OpenAICyberSessionIdentityMetricsSnapshot{
		Total:          openAICyberSessionIdentityMetrics.total.Load(),
		Resolved:       openAICyberSessionIdentityMetrics.resolved.Load(),
		Missing:        openAICyberSessionIdentityMetrics.missing.Load(),
		Conflict:       openAICyberSessionIdentityMetrics.conflict.Load(),
		Invalid:        openAICyberSessionIdentityMetrics.invalid.Load(),
		Inherited:      openAICyberSessionIdentityMetrics.inherited.Load(),
		StrictRejected: openAICyberSessionIdentityMetrics.strictRejected.Load(),
		WSIdentitySwap: openAICyberSessionIdentityMetrics.wsIdentitySwap.Load(),
	}
}
