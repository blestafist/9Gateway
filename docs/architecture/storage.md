# Storage

## Boundaries

Single-instance runtime counters and active reservations live in memory.
Persistent keys, policies, request history, bodies, and aggregates live in
SQLite. Deployment settings and secrets live in YAML/environment variables.
Do not create competing sources of truth.

SQLite uses WAL mode, foreign keys, explicit embedded migrations, and settings
suitable for concurrent reads/writes. Limiter hot paths and streaming flushes do
not perform SQL. Runtime reservations may remain memory-only because process
restart also terminates all upstream requests.

## API Keys

The `api_keys` table stores ID, name, prefix, keyed hash, enabled state,
expiration, timestamps, and policy JSON. It never stores the raw key. Policy JSON
is acceptable initially and avoids premature schema normalization.

## Requests And Bodies

Request history stores request/key identity, route/model/modes, status and
termination, byte and token counts, cost-known state, latency fields, safe error
details, and timestamps. Keep captured client/upstream/response bodies in a
separate table with truncation flags and original sizes. Treat bodies as
sensitive data with independent retention.

## Aggregates And Caches

Usage buckets support reporting and persistence but are not the source of truth
for sub-minute runtime limiting. Cache enabled key hashes and compiled policies
in immutable/atomic memory snapshots; admin changes update SQLite and then the
snapshot. Batch low-value timestamps such as last-used updates.

Critical accounting state updates synchronously in memory. Detailed telemetry
uses a bounded asynchronous queue and may be dropped with a metric when full;
budget-affecting usage may not be lost merely because telemetry is unavailable.

The deployment caps detailed telemetry before persistence: a 128-entry queue
(maximum 4096), a zero-default body capture limit (maximum 1 MiB), 30-day
metadata retention (maximum 365 days), and 7-day body retention (maximum 365
days and never longer than metadata). Cleanup is fixed rather than configurable:
one startup pass and one after each 1024 processed jobs, deleting no more than
1000 body rows and then 1000 metadata rows per pass.
