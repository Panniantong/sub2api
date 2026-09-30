# Official upstream v0.2.11 merge review

Source: `https://github.com/Wei-Shaw/sub2api.git`, `main` at
`42bc7f6cffe24bcb471608e48e66b4a0afa1f882` (VERSION sync after v0.2.11).
Custom baseline: `b6180efec`; backup: `backup/pre-upstream-0.2.11-20260930`.
The sibling local checkout was not used as the upstream source.

## Preserved custom behavior

- CPA HTTP first-byte measurement and stream flushing, independently of semantic output timeouts.
- Original downstream Cookie echo; actual upstream Set-Cookie fallback when no downstream Cookie is supplied.
- Separate upstream and downstream Cookie fields in usage records and exports.
- Bounded asynchronous response Cookie synchronization, acquisition logs, normalized Host deduplication, newest-token comparison, and target-Host refresh.
- Cookie binding, degraded-group scheduling, fixed-Host monitor protection, and existing Cookie/WS configuration UI.

## Merge fixes

- Pass the Cookie scheduling context through the newly added legacy previous-response selection path.
- Combine official per-attempt response cancellation with custom first-byte tracing without decompressing twice.
- Initialize account props before installing the immediate Cookie binding watcher.
- Do not start Cookie workers without their optional settings service; ordinary lightweight gateways previously panicked in background account discovery.
- Keep the fixed-Host monitor guard on full account edits, including billing edits omitting Host; adapt the upstream transaction test and cover omitted-Host writes.
- Make the pre-existing Ollama stale-callback test create an explicitly distinct deadline instead of relying on two clock reads differing on Windows.

## Validation

- Frontend production build and 167 account usage/edit, settings, and usage-view tests passed.
- Repository, DTO, middleware, streamlatency, and migration suites passed (Git shell supplied for pg_dump test doubles on Windows).
- Handler, admin Handler, route, and server wiring suites passed after the Cookie worker fix.
- Focused Cookie acquisition/refresh/scheduling, response sync, downstream Cookie, CPA, streaming terminal, legacy scheduler, and Ollama callback regressions were run.

The full service suite is **not green**. Existing baseline failures concern ticket length/binding expectations, legacy OAuth fast-tier behavior, API-key error Cookie expectations, and WS session/tool-ID behavior. These must not be used to remove the explicitly requested custom behavior. Full logs are retained locally under `.codex-tmp/upstream-merge-full-review.log`; the pre-merge comparison logs are `cookie-sync-baseline.log` and `cookie-sync-full-service.log`.

Migration filenames, not numeric prefixes, are the migration identity. The official `240_affiliate_ledger_operation_id.sql` and custom `240_cookie_validation_history.sql` therefore coexist without renaming an already-applied migration.
