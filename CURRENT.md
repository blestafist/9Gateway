# Current Work

Current milestone: persistent request observability and bounded body inspection
(`T121`-`T140`).

Done: `T001`-`T121`.

Current: `T122` - add request-local trace state.

Queued: `T123`-`T140` in dependency order from `TASKS.md`.

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
critical path. T107-T120 implement budget reservation/reconciliation,
persistent spend, and UTC day/calendar-month
enforcement. T118 adds exact UTC calendar-month bucket identities, restart
restoration, cross-month settlement, and boundary-based Retry-After behavior;
T119 replacement admission scopes active reservations to their captured
total/day/month identities, so replacement cannot import unrelated active
capacity while finalization remains on the admitting buckets;
the optional Bifrost review and Apache-2.0/no-copy provenance are recorded in
the budget implementation comments. T121-T140 add one canonical request trace,
safe structured completion fields, optional per-key bounded
client/upstream/response body capture, SQLite request history, independent body
retention, and a bounded best-effort history writer. Detailed telemetry may be
dropped and must never control transport or accounting. `/metrics`, `/ready`, CLI,
request-history admin APIs, tool-call execution/validation, and Web UI remain out
of scope.
Upstream EOF, not `[DONE]` or `finish_reason`, controls normal transparent stream
completion; exact `[DONE]` may complete only the explicit SSE-to-JSON conversion.

The focused T091-T100 review fixes independently bound compressed wire bytes
during gzip SSE-to-JSON conversion, prevent legacy token checkpoint promotion
from double-counting after restart, serialize token-policy replacement against
admission, and make observation timeout invalidation terminal.

T109 composes concurrency, token, and optional lifetime-budget ownership in one
idempotent lease. Admission is ordered concurrency -> tokens -> budget, with
reverse rollback on later rejection; known, conservative, pre-upstream, and
deferred terminal paths settle token and budget independently. Deferred cleanup
returns separate adjustment tickets and releases concurrency before returning.

T111 carries immutable selected pricing and independent token/budget adjustment
ownership through the bounded response-observation worker. Canonical JSON/SSE
usage is parsed off the response path and actual integer-micros cost replaces
the conservative budget charge only when differentiated usage and pricing are
known; unknown, overflow, malformed, truncated, dropped, and shutdown paths
retain conservative charges. Transparent response bytes, status, headers, and
completion timing remain unchanged.

T112 carries selected pricing into the explicit stream:false/SSE compatibility
path and settles its composite lease synchronously from the canonical usage
produced during aggregation. Actual integer-micros cost replaces the
conservative budget charge before bounded trailer drain or generated-response
write; known token totals still reconcile when differentiated cost is unknown,
while conversion failure remains conservative. No rendered JSON reparsing,
queue, SQL, logging, or extra pre-write parsing was added.

T113 reconciles transparent SSE budget cost from a bounded immutable copy of
only successfully written and flushed wire bytes. Physical upstream EOF closes
the stream without waiting for `[DONE]` or `finish_reason`, then releases
concurrency and settles conservative token/budget charges before one
nonblocking worker handoff. Canonical SSE observation adjusts actual budget
only when differentiated input/output usage and immutable selected pricing are
known; overflow, malformed/incomplete data, unsupported coding, cancellation,
downstream failure, saturation, and shutdown remain conservative.

T110 wires one startup-built immutable pricing resolver and one process budget
limiter into authenticated generation admission. Known chat-completions and
Responses bodies are bounded-inspected once and replayed byte-for-byte; budget
planning and reservation follow token planning in the composite lease. Total
budget rejection is `429 budget_exceeded` without `Retry-After`; metadata,
pricing, and planning failures fail closed before upstream. Admission remains
conservative at the upstream-start boundary; actual monetary reconciliation is
reserved for T111-T114. `/v1/models` and unrestricted generic traffic retain
their budget-free transparent paths.

T114 audits the complete lease lifecycle at the exact `client.Do` boundary:
pre-start exits release zero, while connection/upload/header/read, response
write/flush, conversion, cancellation, unsupported-response, custom-dispatch,
and internal post-start ambiguity paths conservatively settle exactly once.
Cancellation is issued before deferred body and lease cleanup; concurrency is
released before any best-effort reconciliation handoff. Completion records carry
only typed terminal outcome metadata, never prices, reservations, usage, or
headers. Real HTTP and focused race tests cover immediate pre-start reuse,
conservative post-start charging, cancellation, repeated cleanup, and key
isolation without changing transport transparency or token accounting.
