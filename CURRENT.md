# Current Work

Current milestone: transport remediation before transparent SSE.

Done: `T001`-`T023`.

Current: `T024` - prevent upstream credential disclosure.

Next: `T025` - preserve optional ResponseWriter capabilities.

Queued: `T021`-`T040`.

Known issues: the completed raw proxy can currently reflect an upstream
Authorization response header and hide optional `ResponseWriter` capabilities
behind completion logging. Transparent SSE flush, EOF-close latency,
active-stream cancellation, and unrestricted parallel stream behavior are not
yet covered by the required regressions.

Important: complete `T021`-`T025` before adding SSE behavior. Keep `T026`-`T034`
focused on raw transparent streaming and `T035`-`T040` on a bounded generic SSE
parser and test infrastructure. Do not add OpenAI stream observation,
SSE-to-JSON aggregation, authentication, SQLite, limits, budgets, accounting, or
telemetry persistence. Upstream EOF, not `[DONE]` or `finish_reason`, controls
normal transparent stream completion.
