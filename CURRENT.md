# Current Work

Current milestone: token accounting and limits (`T081`-`T100`).

Done: `T001`-`T094`.

Current: `T095` - observe transparent SSE without blocking.

Queued: `T096`-`T100` in dependency order from `TASKS.md`.

Known issues: none.

Important: T094 now carries canonical usage directly from bounded SSE-to-JSON
conversion into synchronous lease finalization, without reparsing the rendered
response. T093 attaches bounded transparent JSON response observation to
the T092 process handoff: transport
can settle deferred conservative accounting before a non-blocking immutable job
submission, while queue drops and bounded shutdown invalidate tickets safely.
T091 performs bounded token preflight for configured keys,
conservatively completing admitted reservations after transport; response usage
observation remains for T093-T095. Transparent SSE remains byte-preserving and independent from the
bounded generic parser. T061-T080 completed transport hardening, SQLite-backed
gateway keys, minimal admin bootstrap, hot-path authentication, model policy,
generic request windows, and per-key concurrency. T081-T100 add token usage,
bounded estimation, reservation/reconciliation, token-window enforcement, and
persistent token aggregates while keeping parsing and SQLite off the transport
critical path. Pricing, budgets, request-history/body persistence, metrics,
`/ready`, CLI, tool-call execution/validation, and Web UI remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.
