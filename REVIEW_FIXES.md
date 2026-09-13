# Pending Review Fixes

Second review pass for `T131`-`T140`. These findings were confirmed against the
current tree after commits `f7e705f` and `e9bb518`. Do not mark the observability
milestone fully reviewed until the fixes and verification below are complete.

## 1. Empty body snapshot aliases recorder storage (blocker)

File: `internal/observability/body.go`, around `snapshot()`.

For a positive bound and an empty retained prefix, `snapshot()` does not clone
the zero-length slice. The returned slice can therefore have `cap(Bytes) > 0`
and share the recorder's preallocated backing array. Appending to one empty
snapshot can mutate storage visible through another snapshot.

Fix:

- Return an independently owned zero-capacity slice for every empty captured
  body, or full-slice-limit it to `[:0:0]` when ownership transfer is safe.
- Preserve the distinction between no capture and a known empty capture through
  `BodySnapshot.Captured`.
- Add tests for untouched and explicit-empty recorders with a positive bound.
- Assert `cap(snapshot.Bytes) == len(snapshot.Bytes)` and that appending to one
  returned empty snapshot cannot affect another.

## 2. Upstream request replay is double-counted (major)

File: `internal/httpserver/server.go`, around upstream `GetBody` wrapping.

The initial upstream body and bodies returned by `GetBody` use the same
`upstreamBodyCapture`. A 307/308 redirect or transport replay reads the same
logical payload again and increases `upstream_request.OriginalSize`; it may also
mark an otherwise in-bound request as truncated.

Fix:

- Record only the first logical upstream request body stream.
- Keep `GetBody` behavior available, but do not feed replay reads into the same
  request-history recorder.
- Add a real HTTP redirect/replay test proving the upstream can resend the body
  while the captured `OriginalSize`, prefix, and truncation state describe one
  logical request body only.
- Preserve `ContentLength`, streaming behavior, close errors, cancellation, and
  the exact pre-`client.Do` accounting boundary.

## 3. Body-sized snapshot copies still delay handler return (blocker)

Files:

- `internal/httpserver/completion_ownership.go`, around `emit()`
- `internal/httpserver/trace.go`, request/response snapshot accessors
- `internal/httpserver/server.go`, immediate completion paths

For completion paths not transferred to the usage-observation worker, `emit()`
runs on the serving goroutine after the upstream response has reached EOF. It
calls `RequestBodySnapshots()` and `ResponseBodySnapshot()`, which copy retained
body prefixes before the handler returns. This includes opaque responses and SSE
whose observation is rejected because of unsupported or ambiguous content
encoding. It can add `O(max_captured_body_bytes)` work to stream close.

Fix:

- Do not materialize or copy body snapshots from the request goroutine after
  transport completion.
- Transfer finalized recorder/buffer ownership through one bounded nonblocking
  asynchronous handoff. Materialize persistence snapshots off the HTTP path.
- Immediate, rejected-observation, opaque, JSON, SSE, and conversion paths must
  use the same no-copy completion rule.
- Queue rejection and shutdown draining must promptly release recorder/buffer
  ownership.
- Add ordering/allocation tests proving handler return, stream EOF, cancellation,
  lease release, and concurrency reuse occur before body materialization.

## 4. Completion log and history admission can diverge (major)

File: `internal/httpserver/completion_ownership.go`, around sink fanout.

`logger.Enqueue(record)` and `history.SubmitRecord(...)` are independent queue
admissions and both results are ignored. If only one queue is full or shutting
down, one sink receives the request while the other drops it.

Fix or contract decision required:

- Prefer one bounded fanout admission that owns the final record and dispatches
  consistently to both sinks.
- If best-effort sinks are intentionally independent, change the acceptance
  wording and tests so "exactly one final record" refers to canonical emission,
  not guaranteed delivery to both independently droppable sinks.
- Do not add synchronous fallback, durable spooling, or any transport wait.
- Add tests for logger-only saturation, history-only saturation, simultaneous
  shutdown, observation drop, parse failure, and immediate completion.

## 5. History shutdown deadline is not actually bounded (major)

File: `internal/httpserver/history_worker.go`, around `Shutdown()`.

After the caller deadline expires, `Shutdown()` cancels the worker context and
then waits unconditionally for `worker.done`. If a repository operation ignores
context cancellation or blocks in an uninterruptible driver call, process
shutdown hangs indefinitely. The current storage-close safety fix is correct,
but the documented bounded-drain claim is conditional rather than enforced.

Fix or contract decision required:

- Preserve the invariant that SQLite is not closed while the worker can use it.
- Make the bound real through an enforceable interruptible repository/connection
  lifecycle, or explicitly document shutdown as waiting for repository context
  compliance and remove unconditional claims that it is deadline-bounded.
- Add a test with a non-cooperative repository. The expected behavior and
  database ownership after timeout must be explicit and safe.

## 6. Worker failure counters mix jobs and retention passes (major)

File: `internal/httpserver/history_worker.go`, around `retentionPass()`.

Retention errors increment `Failed` without incrementing `Processed`. This makes
job counter invariants impossible: startup retention can produce
`Processed=0, Failed=1`, and a successful persisted job followed by failed
retention can produce `Processed=1, Persisted=1, Failed=1`.

Fix:

- Split job persistence failures and retention failures into separate atomic
  counters.
- Preserve exact job invariants, for example:
  `Processed == Persisted + PersistFailed`.
- Add tests for startup retention failure, scheduled retention failure after a
  successful write, write failure, and combined failures.

## 7. Repository panic leaks job ownership and kills worker (major)

File: `internal/httpserver/history_worker.go`, around `process()`.

Body references are cleared only after `repository.Persist()` returns. A panic
skips clearing and counter updates, terminates the worker goroutine, leaves queued
jobs retained, and normally crashes the process.

Fix:

- Defer body-reference clearing before calling the repository.
- Recover repository panics at the worker boundary.
- Count the current job as failed and either continue FIFO processing or perform
  a documented abort-and-drop sequence for queued jobs.
- Add tests for panic with a body-bearing job, queued jobs after the panic,
  counters, memory release, and shutdown.

## 8. Migration 009 upgrade tests do not prove child FK survival (major)

File: `internal/storage/t137_request_bodies_test.go`, version 8 to 9 tests.

The current test proves rows remain readable but does not prove that rebuilding
`requests` preserved the `request_bodies` foreign key and cascade behavior.

Fix tests:

- Verify `PRAGMA foreign_keys = 1` on the test connection.
- Run `PRAGMA foreign_key_check` after upgrading a populated version-8 DB.
- Inspect `pragma_foreign_key_list('request_bodies')` and require target table
  `requests` with `ON DELETE CASCADE`.
- Delete the upgraded request and assert its body rows are removed.
- Verify the rebuilt `requests.api_key_id` FK still uses `ON DELETE SET NULL`.

## 9. Migration 009 rollback test does not preserve populated v8 data (major)

File: `internal/storage/t137_request_bodies_test.go`, failed migration test.

The forced-failure test creates no version-8 request/body data before migration,
so it cannot detect partial data loss during table rebuild rollback.

Fix tests:

- Seed a request containing representative NULL unknowns and integer known-zero
  values.
- Seed a binary/NUL `request_bodies` row.
- Trigger the migration failure.
- Verify schema version remains 8, old schemas/indexes remain intact, all values
  and body bytes are exactly preserved, and `PRAGMA foreign_key_check` passes.

## 10. SQL body-size literals are not checked for exact cap equality (minor)

Files:

- `internal/storage/migrations/008_request_bodies.sql`
- `internal/storage/migrations/009_request_history_constraints.sql`
- storage/config schema tests

The SQL migrations hardcode `1048576`. Existing tests detect a lower SQL limit
but would not detect the SQL limit being raised above
`schema.RequestBodySchemaSafetyMaxBytes`.

Fix:

- Query the final `request_bodies` table definition from `sqlite_master` or parse
  the embedded migration SQL.
- Assert the `length(body) <= N` value equals
  `schema.RequestBodySchemaSafetyMaxBytes` exactly.

## 11. T140 persisted scalar assertions remain weak (minor)

File: `internal/httpserver/t140_observability_test.go`, around
`assertT140PersistedScalars`.

The helper mostly checks that persisted numbers are non-negative. It does not
compare status, delivered bytes, usage, and timing values to the structured log
record or concrete expected values.

Fix tests:

- Compare persisted downstream/upstream statuses, delivered bytes, input/output/
  total usage, known/unknown cost, and timing values with the corresponding final
  structured-log fields and expected HTTP scenario values.
- Preserve the existing body-policy, restart, and independent-retention checks.

## Verification

After fixes:

```text
go fmt ./...
go test ./...
go build ./...
go test -race ./...
```

Also run focused tests for:

- empty snapshot ownership and capacity;
- redirect/replay request capture;
- unsupported-encoding SSE EOF timing;
- queue saturation and two-sink fanout;
- non-cooperative repository shutdown and repository panic;
- populated v8 to v9 upgrade and rollback;
- SQL/config body-size cap equality.

Use one worker for observability/body plus HTTP ownership fixes because these
files overlap. Use a separate worker for storage migration-test hardening. Run a
final independent review after both fixes and update `CURRENT.md` only after all
verification passes.
