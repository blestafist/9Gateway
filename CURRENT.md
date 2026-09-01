# Current Work

Current milestone: transparent streaming transport.

Done: `T001`-`T038`.

Current: `T039` - bound SSE event memory.

Next: `T040` - add a reusable streaming test upstream.

Queued: `T039`-`T040`.

Known issues: parser event-size enforcement and reusable streaming test harness are not yet implemented.

Important: `T021`-`T035` are complete. Keep `T036`-`T040` focused on the bounded
generic SSE parser and test infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
