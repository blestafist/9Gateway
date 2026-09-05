# Current Work

Current milestone: authentication and per-key request/concurrency policy.

Done: `T001`-`T079`.

Current: `T080` - harden the authentication and limits milestone.

Queued: none for the current milestone.

Known issues: none for the current milestone.

Important: transparent SSE remains byte-preserving and independent from the
bounded generic parser. T061-T063 close narrow transport and observability
risks found by the milestone audit. T065-T079 add SQLite-backed gateway keys,
minimal admin bootstrap, hot-path authentication, model policy, generic request
windows, and per-key concurrency. Token accounting, budgets, request-history
persistence, CLI, tool-call execution/validation, and Web UI remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.
