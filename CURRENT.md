# Current Work

Current milestone: transparent streaming transport.

Done: `T001`-`T033`.

Current: `T034` - prove parallel streams are not serialized.

Next: `T035` - define a protocol-neutral SSE parser contract.

Queued: `T034`-`T040`.

Known issues: unrestricted parallel stream behavior is not yet covered by the
required regression.

Important: `T021`-`T033` are complete. Keep `T034` focused on raw
transparent streaming and `T035`-`T040` on a bounded generic SSE parser and test
infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
