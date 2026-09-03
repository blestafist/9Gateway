# Current Work

Current milestone: authentication and per-key request/concurrency policy.

Done: `T001`-`T060`.

Current: `T061` - sanitize transformed response headers.

Next: `T062` - do not wait for EOF after unencoded DONE.

Queued: `T063`-`T080` in dependency order in `TASKS.md`.

Known issues: T061-T063 capture the bounded transport/observability audit findings.

Important: transparent SSE remains byte-preserving and independent from the
bounded generic parser. T061-T063 first close narrow compatibility/observability
risks found by the milestone audit. T064-T080 then add SQLite-backed gateway keys,
minimal admin bootstrap, hot-path authentication, model policy, generic request
windows, and per-key concurrency. Token accounting, budgets, request-history
persistence, CLI, tool-call execution/validation, and Web UI remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.
