# Current Work

Current milestone: transparent streaming transport.

Done: `T001`-`T039`.

Current: `T040` - add a reusable streaming test upstream.

Next: none in the active queue.

Queued: `T040`.

Known issues: the reusable streaming test harness is not yet implemented.

Important: `T021`-`T035` are complete. Keep `T036`-`T040` focused on the bounded
generic SSE parser and test infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
