# Current Work

Current milestone: token accounting and limits (`T081`-`T100`).

Done: `T001`-`T083`.

Current: `T084` - define tokenizer strategy configuration.

Queued: `T084`-`T100` in dependency order from `TASKS.md`.

Known issues: none.

Important: transparent SSE remains byte-preserving and independent from the
bounded generic parser. T061-T080 completed transport hardening, SQLite-backed
gateway keys, minimal admin bootstrap, hot-path authentication, model policy,
generic request windows, and per-key concurrency. T081-T100 add token usage,
bounded estimation, reservation/reconciliation, token-window enforcement, and
persistent token aggregates while keeping parsing and SQLite off the transport
critical path. Pricing, budgets, request-history/body persistence, metrics,
`/ready`, CLI, tool-call execution/validation, and Web UI remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.
