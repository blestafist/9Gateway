# Current Work

Current milestone: authentication and per-key request/concurrency policy.

Done: `T001`-`T062`.

Current: `T063` - decouple completion logging from response close.

Next: `T064` - add storage and credential configuration.

Queued: `T064`-`T080` in dependency order in `TASKS.md`.

Known issues: T063 captures the bounded observability audit finding.

Important: transparent SSE remains byte-preserving and independent from the
bounded generic parser. T061-T062 close narrow transport risks found by the
milestone audit. T063-T080 then add SQLite-backed gateway keys,
minimal admin bootstrap, hot-path authentication, model policy, generic request
windows, and per-key concurrency. Token accounting, budgets, request-history
persistence, CLI, tool-call execution/validation, and Web UI remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.
