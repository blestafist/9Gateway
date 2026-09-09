# Current Work

Current milestone: pricing and budget enforcement (`T105`-`T120`).

Done: `T001`-`T105`.

Current: `T106` - add total budget reservation and reconciliation.

Queued: `T107`-`T120` in dependency order from `TASKS.md`.

Known issues: none. T101 adds exact, unknown-aware integer-USD-micros money
values with checked arithmetic and canonical decimal conversion. T102 adds
strict deployment pricing rules with compiled slash-aware selectors and ordered
immutable accessors. T100 adds the
complete persistent token-accounting lifecycle
scenario, including process restart restoration, reconciliation, reset admission,
transport/error/cancellation coverage, request/concurrency saturation, policy
replacement, and structured-log redaction checks.

Important: T096 makes lease cleanup deterministic at the upstream-start
boundary: pre-start exits release zero usage, while every post-start ambiguity
conservatively settles exactly once. Compatibility conversion commits a valid
already-observed total even when later downstream/drain work fails; transparent
JSON/SSE observation remains conservative-first and asynchronous. T095 observes
transparent SSE only from successfully written and
flushed wire bytes, then hands a bounded immutable copy to the T092 worker at
physical EOF. T094 now carries canonical usage directly from bounded SSE-to-JSON
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
critical path. T106-T120 are planned for budget reservation/reconciliation,
persistent spend, and UTC day/calendar-month
enforcement. Request-history/body persistence, metrics, `/ready`, CLI, tool-call
execution/validation, and Web UI remain out of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.

The focused T091-T100 review fixes independently bound compressed wire bytes
during gzip SSE-to-JSON conversion, prevent legacy token checkpoint promotion
from double-counting after restart, serialize token-policy replacement against
admission, and make observation timeout invalidation terminal.
