# Current Work

Current milestone: completed transport compatibility milestone.

Done: `T001`-`T060`.

Current: none.

Next: none.

Queued: none.

Known issues: none recorded.

Important: transparent SSE remains byte-preserving and independent from the
bounded generic parser. T041-T060 add bounded request inspection, protocol-level
observation, and only the explicit `stream:false` plus actual-upstream-SSE
compatibility path, including preservation of multiple choices and fragmented
tool-call arguments. Authentication, SQLite, limits, budgets, accounting,
tool-call execution/validation, and telemetry persistence remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion.
