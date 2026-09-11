# Active Tasks

This file contains the next twenty atomic tasks. An implementation agent reads
only `AGENTS.md`, `CURRENT.md`, its assigned task, and the architecture documents
linked by that task. Tasks are ordered by dependency and must not be implemented
out of order. Do not load `PLAN.md`, `docs/tasks/backlog-full.md`, or
`docs/archive/ARCHITECTURE-full.md` during routine implementation.

Every implementation task must finish with `go fmt ./...`, `go test ./...`, and
`go build ./...`. Add behavioral tests in the same task as changed HTTP or
streaming behavior. Update `CURRENT.md` and commit after completing one task.

For accounting, limiter, pricing, budget, and observability tasks, first inspect
the equivalent implementation in the optional local `.references/bifrost`
checkout when it is available. Record the inspected Bifrost commit and source
paths in the task commit message or an adjacent source comment/notice. Prefer a
maintained permissive dependency, then a small isolated adaptation, then a clean
minimal implementation. Do not import Bifrost architecture or make the ignored
checkout a build/runtime dependency. Before adapting code or data, verify its
file-level provenance and dependency license chain, preserve required notices,
and mark adapted files.

## Pricing Foundation

### T101 - Define exact money values

Goal: introduce one protocol-independent money representation that cannot lose
precision or overflow when pricing and budgets are added.

Scope:

- Add an immutable `accounting.Money` value measured in integer USD micros,
  where `1 USD = 1_000_000 micros`; never represent money with `float32` or
  `float64` in domain, policy, persistence, or HTTP code.
- Preserve known zero separately from unknown cost. Unknown is a first-class
  state, not an alias for zero, and must propagate through arithmetic unless an
  operation has all required known operands.
- Accept only non-negative known values and provide checked add, subtract, and
  comparison operations suitable for `spent + reserved + candidate` admission.
- Define safe conversion helpers only for integer micros and canonical decimal
  display. Decimal parsing must reject signs, exponent notation, excess precision
  beyond six fractional USD digits, ambiguous separators, and overflow.
- Keep the type independent of pricing rules, models, HTTP, policy, limiter,
  SQLite, configuration, and token protocol parsing.

Acceptance and tests:

- Table tests cover unknown, explicit zero, one micro, whole/fractional USD,
  canonical formatting, maximum representable value, negative input, malformed
  decimal input, excess precision, checked underflow, and checked overflow.
- No successful parse rounds a monetary value, and formatting followed by parsing
  preserves every representable known value.
- Public APIs do not expose mutable internal state or an unchecked raw arithmetic
  path that later budget code could misuse.

Reference: `docs/architecture/accounting.md#pricing`,
`docs/architecture/repository.md#dependencies`.

Dependencies and out of scope: depends on T100. Do not add pricing configuration,
model matching, cost calculation, policy fields, limiter state, or persistence.

### T102 - Validate pricing configuration

Goal: define the strict deployment-level pricing table used to estimate and
reconcile costs without making pricing a runtime database setting.

Scope:

- Add a `pricing.rules` YAML list. Each rule contains `model`,
  `input_per_million_micros`, and `output_per_million_micros`; prices are
  non-negative integer USD micros per one million tokens.
- Treat an exact model string and a model glob as the only supported selectors.
  Reuse or extract the existing slash-aware glob semantics rather than creating
  regex support or subtly different matching rules.
- Preserve declaration order because T103 uses it as deterministic precedence
  among matching glob rules. Reject duplicate exact selectors and duplicate glob
  selectors after compilation; reject empty/invalid UTF-8 patterns, invalid glob
  syntax, null fields, negative prices, values outside T101's safe range, and
  unknown YAML fields.
- Allow an empty table so deployments without budget enforcement remain valid.
  Pricing contains no secret and receives no environment-substitution syntax.
- Compile and validate once during configuration loading, before the listener
  starts; no request may parse YAML or compile a glob.

Acceptance and tests:

- Strict config tests cover an omitted/empty table, exact and glob entries, zero
  prices, ordered rules, duplicate and malformed patterns, missing/null fields,
  negative/overflowing values, and unknown fields at every new level.
- Existing minimal configuration remains valid and receives an empty pricing
  table without invented wildcard prices.
- Configuration errors contain field/rule positions but never upstream/admin
  credentials or complete configuration dumps.

Reference: `docs/architecture/accounting.md#pricing`,
`docs/architecture/operations.md#configuration`,
`docs/architecture/policy.md#effective-policy`.

Dependencies and out of scope: depends on T101. Do not put pricing in SQLite,
add hot reload, aliases, provider routing, cached-token discounts, budgets, or
request-path lookup.

### T103 - Resolve model pricing deterministically

Goal: select one immutable pricing rule for a model with explicit, testable
precedence and an honest unknown result.

Scope:

- Add a pricing resolver in `internal/accounting` (or the smallest adjacent
  package consistent with current boundaries) built from T102's validated rules.
- An exact selector always wins over every glob regardless of declaration order.
  If no exact rule matches, the first matching glob in configuration order wins.
  If nothing matches, return an explicit unknown result rather than a zero-price
  synthetic rule.
- Match the complete model string with the existing slash-aware glob behavior;
  do not strip provider prefixes, lowercase, normalize, alias, or infer models.
- Make the resolver immutable and safe for concurrent lookups. Return copied
  values so request code cannot mutate the process-owned table.
- Keep lookup local and bounded: no SQL, network calls, filesystem reads, locks
  on each request, or provider discovery.

Acceptance and tests:

- Tests cover exact-over-glob, first-glob precedence, overlapping wildcards,
  slash boundaries, escaped metacharacters, Unicode model names, explicit
  zero-price rules, unknown models, and concurrent lookup under `go test -race`.
- Unknown and a matched all-zero rule remain distinguishable.
- Resolver construction cannot accept rules that bypass T102 validation.

Reference: `docs/architecture/accounting.md#pricing`,
`docs/architecture/repository.md#dependencies`.

Dependencies and out of scope: depends on T102. Do not calculate request cost,
read request bodies, query `/v1/models`, add model aliases, or enforce budget.

### T104 - Calculate usage and reservation cost

Goal: calculate exact bounded cost from separate input/output token counts and a
resolved pricing rule.

Scope:

- Implement `ceil((input_tokens * input_rate + output_tokens * output_rate) /
  1_000_000)` in integer arithmetic, using checked multiplication/addition and no
  floating point. Ceiling is required so enforcement never understates a
  fractional micro; round only once after summing both components.
- Calculate actual cost only when both canonical input and output counts are
  known. A known total without both components is insufficient because rates can
  differ; cached/reasoning subset counts are not added again.
- Calculate reservation cost from T086's explicit estimated input and potential
  output components, not from only its combined total.
- Return unknown when pricing is unresolved or required usage components are
  absent. Invalid/overflowing arithmetic returns a safe typed error and never a
  wrapped or zero cost.
- Keep this pure calculation independent of limiter state, HTTP behavior,
  persistence, response mode, and policy decisions.

Acceptance and tests:

- Table tests cover exact-million rates, fractional-micro ceiling, combined
  rounding versus per-component rounding, zero tokens/rates, maximum safe values,
  multiplication/addition overflow, missing input/output, total-only usage,
  cached/reasoning subsets, and unknown pricing.
- Equivalent JSON and SSE canonical usage produce the same known actual cost.
- Errors and formatted values contain no model request body or credential data.

Reference: `docs/architecture/accounting.md#usage-and-estimation`,
`docs/architecture/accounting.md#pricing`.

Dependencies and out of scope: depends on T081-T086 and T101-T103. Do not add
upstream monetary metadata parsing, policy, reservations, SQLite, or telemetry.

## Total Budget Enforcement

### T105 - Add total budget policy

Goal: add a strict per-key lifetime budget contract while preserving every
existing key policy and admin full-replacement behavior.

Scope:

- Add `budget_limits` to stored policy JSON as a list whose first supported entry
  is `{ "amount_micros": N, "period": "total" }`. The list form is intentional
  preparation for T117-T118; in this task reject `day` and `month` rather than
  silently accepting unenforced values.
- Require positive integer micros, exactly one entry per period, valid non-null
  fields, and strict rejection of duplicates, unknown fields, decimals, strings,
  and overflow. Empty/absent limits mean budget-unrestricted.
- Compile budget values into immutable `auth.EffectivePolicy`; expose copied
  storage-independent values and preserve absence separately from zero.
- Extend admin full-policy replacement and responses so a valid total budget is
  validated, persisted, published atomically with the other policy, and survives
  reopen. Invalid replacement leaves SQLite and the auth snapshot unchanged.
- Pricing remains deployment configuration. Never serialize rates or a pricing
  rule into per-key policy.

Acceptance and tests:

- Policy and real admin HTTP tests cover valid combined policy, absent/empty
  budget, exact integer boundaries, duplicate total entries, zero/negative/null,
  unsupported periods, unknown fields, idempotent replacement, immediate snapshot
  visibility, and persistence after reopen.
- Existing model, request, token, concurrency, authentication, and token-policy
  replacement semantics remain unchanged.
- Admin output and errors contain no raw key, digest, pepper, pricing table, or
  credential.

Reference: `docs/architecture/policy.md#effective-policy`,
`docs/architecture/accounting.md#budget`,
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T104 and existing policy/admin flow.
Do not add counters, admission, day/month support, persistence aggregates, a new
budget-specific admin route, or pricing overrides per key.

### T106 - Build conservative cost plans

Goal: turn an admitted token reservation plan and resolved model price into one
safe budget reservation amount before upstream work.

Scope:

- Add a pure budget planner that resolves pricing for the request model and uses
  T104 to price T086's input/output reservation components.
- A key with no budget skips this planner. A key with any budget must fail closed
  when model metadata is missing/malformed/oversized, pricing is unknown, the
  price calculation overflows, or no positive cost can safely represent the
  request; never forward budget-governed work as an implicit zero-cost request.
- A deliberately configured all-zero matched pricing rule produces known zero
  cost and is admissible without inventing a one-micro reservation. Preserve its
  distinction from unknown pricing.
- Return the selected rule identity, known reservation cost, and source metadata
  needed by later traces, without retaining request bytes or a mutable rule.
- Keep HTTP status selection and limiter state outside the planner.

Acceptance and tests:

- Tests cover exact/glob/zero-price rules, unknown/empty/malformed models,
  estimate and fallback token plans, absent output limits, calculation overflow,
  and model strings at configured bounds.
- Every nonzero-priced successful plan has positive known reserved micros; every
  unknown case fails closed only when budget policy requires a plan.
- Input plans/rules are not mutated and errors contain no body fragments.

Reference: `docs/architecture/accounting.md#pricing`,
`docs/architecture/accounting.md#budget`,
`docs/architecture/repository.md#request-orchestration`.

Dependencies and out of scope: depends on T103-T105 and T086. Do not reserve
state, return HTTP errors, observe actual usage, persist spend, or support an
upstream-reported cost.

### T107 - Reserve lifetime budget atomically

Goal: atomically prevent concurrent requests for one key from collectively
exceeding its total budget.

Scope:

- Add an in-memory budget limiter keyed only by stable key ID. Track lifetime
  spent separately from active reservations using T101 money values.
- Admit when `spent + active reservations + candidate <= total limit`; reject
  atomically otherwise. Empty policy remains unlimited. A known zero candidate
  is a valid no-op reservation.
- Return an immutable reservation carrying the exact admitted amount and budget
  identity. Do not expose raw mutable counters.
- Use checked arithmetic throughout. Corrupt/overflowing state fails closed and
  cannot partially reserve capacity.
- Inspect Bifrost budget/concurrency algorithms before implementation, but retain
  this gateway's single-process reservation semantics and document whether any
  isolated logic was adapted.

Acceptance and tests:

- Unit tests cover exact capacity, one-micro rejection, zero cost, separate keys,
  unlimited keys, already-spent capacity, overflow, and deterministic cleanup.
- Barrier-based concurrent attempts cannot collectively reserve above the total
  under `go test -race`; rejected admission changes neither spent nor reserved.
- Limiter operations perform no SQL, parsing, logging, network, or HTTP work.

Reference: `docs/architecture/accounting.md#budget`,
`docs/architecture/policy.md#lease`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T101 and T105-T106. Do not reconcile
actual cost, compose token/concurrency leases, persist spent, or add periods.

### T108 - Reconcile budget reservations once

Goal: settle each admitted budget reservation exactly once without leaking,
double-spending, or incorrectly refunding ambiguous upstream work.

Scope:

- Add idempotent known-cost commit, conservative completion, and proven
  pre-upstream release. Known commit replaces the reservation with actual cost,
  whether lower or higher; over-budget actual cost becomes recorded debt that
  blocks later admission rather than being truncated.
- Conservative completion commits the full reserved cost when upstream may have
  started and actual cost is unknown. Pre-upstream release commits zero.
- Add deferred settlement equivalent to token adjustment tickets: atomically
  commit the conservative reservation and return a one-shot ticket that may later
  replace it with known actual cost. Dropped/invalidated tickets retain the safe
  conservative charge.
- Ensure repeated/concurrent terminal operations return the first result and
  never underflow reserved/spent values. Invalid actual cost settles
  conservatively and returns a typed safe error.
- Emit a narrow committed-spend delta only after in-memory state changes. Its sink
  contract must be nonblocking and must not be required for limiter correctness.

Acceptance and tests:

- Tests cover refund, exact match, actual-over-reservation debt, zero-cost actual,
  conservative completion, pre-start release, deferred lower/higher adjustment,
  invalidation, double/concurrent finalization, sink ordering, sink re-entry, and
  arithmetic faults.
- Active reservations always reach zero exactly once; failed adjustment leaves
  the conservative charge intact.
- Finalization and sink notification do not parse usage, send HTTP, or perform
  SQLite work.

Reference: `docs/architecture/accounting.md#budget`,
`docs/architecture/policy.md#lease`,
`docs/architecture/storage.md#boundaries`.

Dependencies and out of scope: depends on T107. Do not integrate request
transport, calculate prices inside the limiter, persist deltas, or add periods.

### T109 - Compose budget into request lease

Goal: make concurrency, token, and budget reservations one idempotent ownership
unit so partial admission and every cleanup path settle all resources together.

Scope:

- Extend the existing `ResourceLeaseCoordinator` and `ResourceLease` rather than
  introducing a parallel HTTP lifecycle abstraction. Acquire in documented order:
  concurrency, tokens, then budget.
- Roll back only newly acquired refundable resources if a later stage rejects:
  token rejection releases concurrency; budget rejection releases token
  reservation before upstream and concurrency. Request-count consumption remains
  outside the lease and is not refunded.
- Add budget to known, conservative, pre-upstream, and deferred outcomes. Known
  completion accepts canonical usage/cost information sufficient to settle both
  token and budget reservations without one succeeding twice if the other errors.
- Deferred transport completion must release concurrency immediately, commit both
  conservative reservations, and return independent one-shot adjustment ownership
  suitable for the bounded usage worker; it must not retain the composite lease.
- Preserve provisional concurrency during bounded request inspection and all
  concurrency/token-only callers.

Acceptance and tests:

- Tests cover every unlimited/configured combination, each rejection stage,
  rollback ordering, known/conservative/pre-start/deferred outcomes, zero-price
  budgets, repeated concurrent cleanup, and immediate capacity reuse.
- No failure can retain only one of concurrency, token, or budget while returning
  rejection; no active counter underflows.
- Existing token lifecycle tests remain behaviorally valid after the focused
  extension.

Reference: `docs/architecture/policy.md#lease`,
`docs/architecture/repository.md#request-orchestration`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T090 and T108. Do not add HTTP
admission, usage parsing, persistence, daily/monthly periods, or telemetry.

### T110 - Enforce budget preflight admission

Goal: resolve pricing and reserve budget after request/model checks but before
`client.Do` can start upstream work.

Scope:

- Wire the immutable pricing resolver, T106 plan, budget limiter, and T109 lease
  into authenticated `/v1/*` orchestration. Budget-governed generation requests
  require bounded model/token metadata and known pricing; unrestricted requests
  retain the current minimal transparent path.
- Reuse the existing inspected byte-for-byte replay for known JSON chat/Responses
  requests. Unknown endpoints and uninspectable requests under a budget fail
  closed before upstream because a model price cannot safely be selected; do not
  deserialize/rewrite bodies or accept a client-supplied price.
- Keep known non-generating `GET /v1/models` budget-free, matching token behavior.
- Return OpenAI-style HTTP 429 `budget_exceeded` for insufficient available
  budget. Return a safe controlled gateway error for unknown pricing/metadata;
  neither rejection reaches upstream. Do not provide `Retry-After` for lifetime
  budget because no time reset exists.
- Preserve request-window no-refund semantics and the established
  `client.Do` boundary between zero release and conservative finalization.

Acceptance and tests:

- Real HTTP tests cover exact/glob/zero prices, exact capacity, budget rejection,
  token-before-budget rejection, separate keys, concurrent reservations, unknown
  model/price, malformed/oversized body, `/v1/models`, and zero upstream calls for
  every rejected request.
- Admitted request method/path/query/headers/body and cancellation remain
  unchanged; no pricing or budget headers are added downstream.
- No-budget keys show no budget lookup/reservation allocation on the hot path.

Reference: `docs/architecture/repository.md#request-orchestration`,
`docs/architecture/accounting.md#budget`,
`docs/architecture/transport.md#request-body`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T091-T096 and T103-T109. Do not
reconcile actual cost yet, persist spend, support generic unknown endpoint
pricing, add retries, or interrupt streams.

## Actual Cost Reconciliation

### T111 - Reconcile transparent JSON cost

Goal: replace conservative budget charges with actual JSON response cost without
changing response bytes, status, headers, or completion timing.

Scope:

- Extend the bounded usage-observation job/ticket contract to carry immutable
  selected pricing and both token/budget adjustment ownership, never a lease,
  request headers, raw key, model body, or unbounded bytes.
- Reuse T082 canonical JSON usage off the response path, calculate T104 actual
  cost only when input/output and pricing are known, then adjust token total and
  budget cost independently exactly once.
- Missing/partial/invalid usage, total-only usage, unsupported coding, overflow,
  capture truncation, downstream failure, parser failure, queue saturation, and
  shutdown retain the conservative budget charge.
- Preserve identity/gzip wire and decoded bounds. Client delivery and concurrency
  release occur before nonblocking worker submission exactly as in T093.
- Keep upstream 4xx/5xx byte-transparent and use the same known/unknown accounting
  rule rather than assuming errors cost zero.

Acceptance and tests:

- Real HTTP tests cover actual below/equal/above reserve, zero cost, missing one
  token component, total-only usage, absent/invalid usage, gzip, overflow,
  over-bound body, upstream errors, blocked worker, queue drop, and shutdown.
- Known actual usage/cost adjusts each limiter once; every unknown case leaves no
  active reservation or concurrency slot and retains both conservative charges.
- A blocked pricing calculation/worker cannot delay body bytes or EOF, and
  responses remain byte/header/status identical.

Reference: `docs/architecture/accounting.md#pricing`,
`docs/architecture/observability.md#telemetry`,
`docs/architecture/transport.md#generic-passthrough`.

Dependencies and out of scope: depends on T092-T093, T104, and T110. Do not
persist request history, rewrite JSON, parse upstream monetary metadata, or add
period budgets.

### T112 - Reconcile converted SSE cost

Goal: calculate and commit actual cost from canonical usage already produced by
the explicit SSE-to-JSON compatibility path.

Scope:

- Carry the immutable selected pricing/budget settlement data beside the existing
  conversion lease without reparsing rendered JSON or changing aggregation output.
- After aggregation has valid canonical input and output usage, calculate actual
  cost and synchronously finalize token and budget reservations through T109's
  bounded in-memory known outcome. If cost cannot be known, settle budget
  conservatively while still allowing known token-total reconciliation.
- Preserve already-observed valid cost when later bounded drain or downstream
  write fails. Conversion failure before sufficient usage remains conservative
  after upstream start.
- Keep exact `[DONE]`, EOF-without-DONE, gzip trailer validation, compressed-wire
  bounds, tool-call aggregation, header sanitation, and cancellation unchanged.
- No queue, SQL, logging, or additional parsing may be introduced before the
  generated response write.

Acceptance and tests:

- Real HTTP tests cover usage before/after finish reason, exact `[DONE]`, clean
  EOF, actual lower/higher, missing input/output, zero-price rule, malformed SSE,
  valid/corrupt gzip, bounded-drain failure, downstream failure, and cancellation.
- Token and budget reservations settle once on every path; known token total may
  reconcile even when differentiated cost remains unknown.
- Existing Bifrost mismatch, fragmented tool-call, and blocked-after-DONE
  regressions remain valid with unchanged generated JSON.

Reference: `docs/architecture/streaming.md#sse-to-json`,
`docs/architecture/accounting.md#pricing`,
`docs/architecture/accounting.md#budget`.

Dependencies and out of scope: depends on T094, T104, and T109-T111. Do not alter
transparent SSE, synthesize missing usage, persist spend, or parse tool arguments.

### T113 - Reconcile transparent SSE cost

Goal: adjust budget from actual SSE usage while preserving the direct
`read -> write -> flush -> EOF` transport contract.

Scope:

- Extend T095's request-local bounded side capture and T092 worker job with
  immutable pricing and budget adjustment ownership. Copy bytes only after a
  successful downstream write and flush; never wait for event framing first.
- At physical upstream EOF, release concurrency and conservatively commit token
  and budget through the deferred lease outcome, then submit one nonblocking job.
- Worker-side T083 observation calculates cost only from known canonical input and
  output. `[DONE]`, finish reason, and usage events remain metadata and never
  control transparent transport lifetime.
- Capture overflow, malformed/incomplete SSE, missing differentiated usage,
  unsupported coding, downstream failure, queue saturation, and cancellation keep
  the conservative budget charge.
- Requests without token or budget observation needs retain the allocation-minimal
  streaming path and create no parser job or per-chunk goroutine.

Acceptance and tests:

- Real HTTP timing tests cover usage with/without `[DONE]`, usage-only terminal
  events, split/coalesced reads, immediate EOF, lower/higher actual cost, missing
  components, gzip, blocked worker, overflow, malformed events, and cancellation.
- Client bytes, first flush, per-fragment delivery, EOF close, and unrestricted
  parallelism remain within existing regression thresholds.
- No worker or pricing operation backpressures transport; completion leaves no
  active lease, token reservation, or budget reservation.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/streaming.md#termination`,
`docs/architecture/observability.md#telemetry`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on T095, T104, and T109-T111. Do not
buffer before delivery, stop on protocol terminal metadata, add timers, persist
body content, or enforce approximate cost mid-stream.

### T114 - Finalize every budget lifecycle path

Goal: prove deterministic budget cleanup at the exact upstream-start boundary for
all success, error, cancellation, and internal exit paths.

Scope:

- Apply the existing rule uniformly: only exits before `client.Do` release budget
  at zero; every post-start ambiguity conservatively commits reserved cost unless
  valid actual differentiated usage was already observed.
- Audit upstream construction/connection/upload/header/read errors, transparent
  JSON/SSE writes and flushes, aggregation/decoding/draining, client cancellation,
  unsupported responses, custom dispatch hooks used by tests, and internal early
  returns.
- Ensure cancellation reaches upstream before lifecycle cleanup finishes.
  Reconciliation handoff must never delay downstream EOF or retain concurrency.
- Make token and budget partial knowledge independent: known total can settle
  token accounting while unknown input/output leaves budget conservative.
- Expose only safe typed terminal metadata to current completion logs; never emit
  reservation amounts, prices, or usage in response headers.

Acceptance and tests:

- Real HTTP tests prove immediate budget reuse after pre-start failure and
  conservative charging after every ambiguous post-start failure.
- Repeated/concurrent cleanup cannot leak, double-finalize, underflow, or make one
  key affect another; focused lifecycle tests pass under `go test -race`.
- Request-window behavior, response transparency, SSE timing, and token accounting
  remain unchanged.

Reference: `docs/architecture/policy.md#lease`,
`docs/architecture/accounting.md#budget`,
`docs/architecture/repository.md#request-orchestration`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T110-T113. Do not retry, add hard
mid-stream termination, persist spend, add periods, or create request history.

## Persistent And Periodic Budgets

### T115 - Add persistent budget schema

Goal: persist committed budget spend for restart-safe enforcement without putting
active reservations or transport work in SQLite.

Scope:

- Add the next embedded migration for `budget_buckets`, keyed by stable API key,
  period kind, and canonical period start. Store committed `spent_micros` and
  timestamps as checked signed integers; active reservations remain memory-only.
- Define durable identities for `total`, UTC calendar `day`, and UTC calendar
  `month` now because their boundary representation is a schema concern. Use one
  canonical sentinel start for total and explicit epoch-second starts for day and
  month; do not implement day/month runtime admission in this task.
- Add foreign keys, uniqueness, checks, and indexes needed to restore current
  spend by key/period and clean expired calendar rows without scanning request
  history.
- Never store raw keys, pricing rules, prompts, responses, model payloads, token
  estimates, active lease IDs, or per-SSE-event data.
- Preserve transactional/idempotent migration behavior, WAL/foreign keys, schema
  version advancement, upgrade from current databases, and future-version
  rejection.

Acceptance and tests:

- Fresh and upgraded databases expose the exact table, checks, foreign key,
  unique identity, and indexes; reopen is idempotent and failed migration rolls
  back completely.
- Constraints reject negative spend, unknown periods, invalid/noncanonical period
  starts, orphan keys, duplicate identities, and overflowing timestamp shapes.
- Schema inspection proves credentials and body content are absent.

Reference: `docs/architecture/storage.md#aggregates-and-caches`,
`docs/architecture/storage.md#boundaries`,
`docs/architecture/accounting.md#budget`.

Dependencies and out of scope: depends on T114 and current migration runner. Do
not add repositories/workers, restore runtime state, persist active reservations,
or implement daily/monthly admission.

### T116 - Persist and restore total spend

Goal: make lifetime budget enforcement survive graceful restart while SQLite
latency remains outside admission and response transport.

Scope:

- Add a narrow budget repository that atomically applies positive/negative spent
  deltas, loads total buckets deterministically, and rejects underflow,
  corruption, unknown keys, or unrepresentable values.
- Connect T108 committed deltas and deferred adjustments to a process-owned
  coalescing accumulator modeled on proven T099 boundaries: in-memory state changes
  synchronously; a nonblocking wakeup lets one worker batch pending deltas.
- Coalesce by exact bucket identity so a full signal channel cannot lose critical
  spend. Writer failure retains pending state and retries without blocking
  transport or limiter admission.
- Before the listener starts, load lifetime spend, validate it against current
  policies without forgiving over-budget debt, and initialize the budget limiter.
  Restore no active reservation.
- On graceful shutdown, stop new requests, finish accounting observation, then
  flush budget deltas within a bound before SQLite closes. Define ordering with
  the existing token accumulator and usage worker so late adjustments are not
  submitted after their persistence sink stops.

Acceptance and tests:

- Integration tests cover atomic delta application, negative adjustment,
  batching/coalescing, writer failure/recovery, multiple keys, reopen/startup
  restore, already-over-budget state, and bounded shutdown.
- A blocked SQLite writer cannot delay admission, JSON response, SSE flush, SSE
  EOF, cancellation, or concurrency reuse; in-memory enforcement remains correct.
- Graceful restart preserves spent amount exactly and restores no stale active
  resource. Logs/errors reveal no key, credential, pricing table, or SQLite data.

Reference: `docs/architecture/storage.md#aggregates-and-caches`,
`docs/architecture/operations.md#server-lifecycle`,
`docs/architecture/observability.md#telemetry`.

Dependencies and out of scope: depends on T108 and T115. Do not make SQLite the
live limiter source of truth, promise crash-proof distributed accounting, add
request history, or implement daily/monthly admission.

### T117 - Enforce UTC daily budgets

Goal: add restart-safe per-key calendar-day budgets without treating a day as a
rolling duration or weakening total-budget enforcement.

Scope:

- Accept one `{ "period": "day", "amount_micros": N }` policy entry alongside
  optional total. Define a day as `[00:00:00, next 00:00:00)` in UTC; it is not a
  sliding 24-hour window and does not depend on host local timezone.
- Extend the budget limiter/reservation identity so one request atomically passes
  and reserves every configured total/day limit or none. A reservation keeps the
  exact day bucket that admitted it even if completion crosses midnight.
- On rejection return the daily reset as positive rounded-up `Retry-After`; when
  total also rejects, omit reset if no time alone can make admission possible.
- Persist and restore current daily spent through T115-T116 plumbing. Expired day
  rows can be lazily/runtime and asynchronously/storage cleaned only after active
  reservations no longer reference them.
- Use the existing injectable clock. Clamp or fail safely on backwards clock
  movement so it cannot reopen an earlier budget bucket and grant extra spend.

Acceptance and tests:

- Tests cover UTC boundary, leap day, exact reset, crossing-midnight reservation,
  total+day atomic rejection, separate keys, restart before/after reset, cleanup,
  backwards time, and concurrent admission under `go test -race` without sleeps.
- Policy/admin tests prove invalid or duplicate day entries do not alter stored or
  published policy.
- No rejected request reaches upstream and no completed request is charged to a
  newer day than the one that admitted it.

Reference: `docs/architecture/accounting.md#budget`,
`docs/architecture/testing.md#limit-tests`,
`docs/architecture/storage.md#aggregates-and-caches`.

Dependencies and out of scope: depends on T105-T116. Do not add rolling-day,
hourly, timezone-selectable, or calendar-month budgets.

### T118 - Enforce UTC calendar-month budgets

Goal: add restart-safe monthly budgets using real UTC calendar boundaries rather
than a fixed 30-day duration.

Scope:

- Accept one `{ "period": "month", "amount_micros": N }` entry alongside
  optional total/day limits. Define a month from UTC first-of-month midnight to
  the next UTC first-of-month midnight.
- Extend atomic multi-limit reservation/reconciliation and persistence identities
  to the exact calendar month captured at admission. Completion in a later month
  still settles the admission month.
- Compute `Retry-After` from the actual next month boundary with checked duration
  conversion and positive rounding. If total budget is also exhausted, do not
  advertise a misleading monthly reset.
- Restore only current calendar buckets before serving, preserve over-budget debt,
  and clean expired month rows without blocking transport or deleting active
  admission identities.
- Keep UTC fixed for MVP; do not add tenant timezones, billing anchors, proration,
  cron syntax, or a 30-day approximation.

Acceptance and tests:

- Fake-clock tests cover 28/29/30/31-day months, December-to-January, leap year,
  exact boundary, cross-month completion, total+day+month atomic admission,
  backwards time, restart, cleanup, and concurrency under `go test -race`.
- Real HTTP tests verify monthly rejection, accurate `Retry-After`, zero upstream
  calls, and unaffected transparent JSON/SSE behavior.
- Invalid/duplicate month policy leaves persistent and published policy unchanged.

Reference: `docs/architecture/accounting.md#budget`,
`docs/architecture/testing.md#limit-tests`,
`docs/architecture/operations.md#server-lifecycle`.

Dependencies and out of scope: depends on T117. Do not add arbitrary duration,
rolling month, local timezone, organization/global budgets, or usage reporting API.

### T119 - Make budget policy replacement safe

Goal: serialize admin budget-policy changes against admission without erasing
spent money or allowing stale snapshots to bypass a newly committed limit.

Scope:

- Add a budget-limiter policy replacement transaction analogous to the hardened
  token replacement boundary, but preserve spend by key and period identity rather
  than treating the configured amount as spent-state identity.
- Hold old-policy admissions out while validating, durably replacing policy, and
  publishing the new auth snapshot. A request holding an older principal snapshot
  after commit must fail admission rather than use removed/increased limits.
- Changing an amount preserves current period spend. Lowering below spent is
  allowed and blocks new priced requests until reset (or forever for total);
  increasing makes only the difference available. Removing a limit stops
  enforcement but does not delete historical spend, so re-adding it cannot reset
  accounting.
- Active reservations continue settling against their captured bucket even when
  the policy changes. Do not strand reservations or move charges into the new
  period/limit.
- Repository failure leaves limiter generation, auth snapshot, and policy JSON
  unchanged. Snapshot publication failure must not expose a durable/runtime split.

Acceptance and tests:

- Race tests cover replacement versus admission/finalization, lower/increase,
  remove/re-add, total/day/month combinations, stale principals, active
  reservations crossing replacement, and persistence/restart.
- No request can be admitted under stale budget after successful replacement; no
  spend or reservation is reset, duplicated, or moved.
- Existing token-policy replacement, admin authentication, and secret-redaction
  tests remain valid.

Reference: `docs/architecture/policy.md#effective-policy`,
`docs/architecture/policy.md#lease`,
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T105-T118 and existing admin policy
replacement. Do not add PATCH semantics, a budget reset endpoint, deletion of
spend, pricing hot reload, or request history.

## Milestone Gate

### T120 - Complete the budget milestone

Goal: validate pricing and total/daily/monthly budget enforcement end to end
without regressing transparent transport, token accounting, persistence, or
administration.

Scope:

- Add one real-HTTP lifecycle scenario with at least two persistent keys and
  overlapping request/token/concurrency/total/day/month policies across process
  restart. Cover pricing exact/glob selection, conservative reservation,
  concurrent contention, actual reconciliation, restored spend, period reset,
  over-budget debt, and policy replacement.
- Exercise ordinary JSON, transparent SSE, SSE-to-JSON, zero-priced models,
  unknown price, missing/partial/invalid usage, actual below/above reservation,
  upstream error, client cancellation, malformed/oversized requests, and all
  existing rejection types.
- Assert every policy rejection makes zero upstream calls and that raw keys,
  admin/upstream credentials, pepper, prompt/response fragments, digests, pricing
  internals, reservation values, and SQLite details do not appear in gateway
  responses or captured structured logs.
- Deliberately block usage observation and SQLite token/budget writers to prove
  they cannot delay first byte, per-fragment flush, downstream EOF, cancellation,
  or concurrency reuse. Critical in-memory accounting must remain correct.
- Run the full race suite and fix confirmed races, deadlocks, worker leaks,
  reservation leaks, persistence loss on graceful shutdown, and timing regressions
  in this task. Update immediate architecture wording only where implementation
  made it stale.

Acceptance and tests:

- Concurrent requests cannot oversubscribe any request/token/budget/concurrency
  policy; every completion/error/cancel path eventually releases active capacity
  while preserving the documented conservative charges.
- Restart preserves token usage and budget spend but no active lease; UTC day and
  real calendar-month reset semantics are deterministic with fake clocks.
- Transparent traffic preserves method, escaped path, query, safe headers, body,
  status, response headers/bytes, first flush, and EOF timing except for the
  existing explicit SSE-to-JSON conversion.
- `go test -race ./...`, `go fmt ./...`, `go test ./...`, and `go build ./...`
  pass. `CURRENT.md` marks T101-T120 done and leaves the next milestone unset.

Reference: `docs/architecture/testing.md#transport-integration`,
`docs/architecture/testing.md#limit-tests`,
`docs/architecture/testing.md#security-and-performance`,
`docs/architecture/accounting.md#budget`.

Dependencies and out of scope: depends on T101-T119. Do not add request-history
or body persistence, Prometheus, `/ready`, CLI, Web UI, provider routing,
protocol translation, Redis, PostgreSQL, or retries.
