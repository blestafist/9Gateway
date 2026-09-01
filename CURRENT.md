# Current Work

Current milestone: transparent streaming transport.

Done: `T001`-`T030`.

Current: `T031` - preserve DONE, comments, and raw SSE framing.

Next: `T032` - preserve split and coalesced SSE bytes.

Queued: `T031`-`T040`.

Known issues: active-stream cancellation and unrestricted parallel stream behavior
are not yet implemented or covered by the required regressions.

Important: `T021`-`T030` are complete. Keep `T031`-`T034` focused on raw
transparent streaming and `T035`-`T040` on a bounded generic SSE parser and test
infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
