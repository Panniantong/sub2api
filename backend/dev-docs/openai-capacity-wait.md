# OpenAI capacity admission

Ordinary session affinity no longer queues behind a full account while other
eligible accounts have capacity. Both schedulers spill over to idle accounts;
the original conversation binding is retained for temporary capacity spillover.
Legacy scheduling also tries alternatives with load batching disabled. Before
queuing, advanced scheduling checks beyond the preferred Top-K, retaining its
existing probe budget, and legacy scheduling refreshes a cached full load view.

Ordinary wait plans request a full reschedule after a short jittered delay
(about 180–270 ms). The caller repeats the original selection with the same
group, model, capabilities and failure exclusions. Capacity polling does not
consume upstream retry attempts. Each wait slice retains the existing account
wait-count admission limit and releases its count on completion or cancellation.
This is not a new FIFO queue or a distributed group queue.

The request stores an absolute deadline at its first account wait. Subsequent
waits and retries cannot extend it; ordinary pool waits use the existing fallback
timeout. Expired late acquisitions are released. The deadline is not applied to
the admitted upstream stream. SSE heartbeats continue across polling slices.

Stateful previous-response routing and protected task-owner/guardian selection
retain account-specific admission. Existing WebSocket turns retain their owner;
movable initial WebSocket admission can retry the pool without emitting HTTP/SSE
data on the upgraded connection. No new configuration or Cookie-specific policy
is introduced. Anthropic/Gemini schedulers are outside this change.

Regression coverage includes both scheduler modes, load batching on/off, full
sticky spillover, recovered capacity outside Top-K, fixed deadlines across
account changes, cancellation and queue cleanup, and stateful ownership.

Ordinary OpenAI sessions also try a less loaded account before the owner fills:
the owner's actual concurrency must be at least 70% of its configured limit,
and the alternative at least 30 percentage points lower. Only a successful slot
acquisition causes spillover; otherwise the owner remains available immediately.
Missing/failed load reads never count as idle. No Cookie state or quota preference
controls this rule. Existing eligibility, final binding checks and protected
ownership still apply; the durable session binding is not rewritten.

Advanced OpenAI selection handles a load spread of at least 30 percentage points
before Top-K scoring: candidates within 10 points of the lowest known load are
tried first, then busier candidates as fallback. Compact capability tiers remain
intact. Cookie quota priority applies only to Host preparation, never request
candidate ordering. These admission heuristics do not guarantee upstream TTFT.
