# Current Work

Current milestone: transparent streaming transport.

Done: `T001`-`T036`.

Current: `T037` - handle SSE comments and field syntax safely.

Next: `T038` - parse split and coalesced input reads.

Queued: `T037`-`T040`.

Known issues: parser read-boundary handling and event-size enforcement are not yet implemented.

Important: `T021`-`T035` are complete. Keep `T036`-`T040` focused on the bounded
generic SSE parser and test infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
