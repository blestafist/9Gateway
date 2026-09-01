# Current Work

Current milestone: transparent streaming transport.

Done: `T001`-`T034`.

Current: `T035` - define a protocol-neutral SSE parser contract.

Next: `T036` - parse SSE data lines and event names.

Queued: `T035`-`T040`.

Known issues: bounded generic SSE parser behavior is not yet implemented.

Important: `T021`-`T034` are complete. Keep `T035`-`T040` focused on the bounded
generic SSE parser and test infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
