# Retry budget and Cookie dispatch review

Local changes only; not deployed. Database configuration is unchanged.

HTTP Responses, Chat Completions and Messages use a 25-second pre-output budget (0 disables). The initial account may be attempted twice, followed by five backup accounts once each. Abandoned accounts cannot re-enter. 429 retries wait at most 2 seconds; other transient retries at most 1 second. A longer Retry-After causes account switching rather than an early retry.

Cookie mailbox cooldown reads have a 5-second, 4096-entry cache and singleflight. Local writes invalidate it; generation checks reject stale inflight reads. Cross-process staleness can reach 5 seconds.

Cookie dispatch failures now carry cookie_binding_unavailable and skip same-account retries. Normal rotation groups reject missing or expired Hosts. Early rotation with a valid binding remains eligible. Explicit degraded groups retain exhausted-Host behavior. Tests cover these cases and account-slot release on admission rejection.

Account slots release when each forwarding attempt returns. User slots remain held across retries. Live inspection found thousands of retries over three minutes, including 8-second 429 waits. The new budget has not been deployed.

Rotation still holds a PostgreSQL transaction advisory lock during network validation. Five idle-in-transaction lock holders were observed, the oldest approximately 26 seconds. Deadline failures also prevent some rotation state/schedule writes, so persisted running state may be stale. This patch does not replace that distributed lock or alter rotation persistence.

Focused tests cover dispatch, scheduling, degradation, handler budgets, repository caching and configuration. Full repository tests require sh for some pg_dump tests on Windows; full service tests have separately reproduced baseline failures.

Limitations: 25 seconds can reject legitimate long reasoning or non-streaming requests. Dedicated WebSocket and image routes are not covered. Slow inbound reads and blocked downstream writes are not guaranteed to stop at that deadline. Unusual SSE events need further output-classifier review.
