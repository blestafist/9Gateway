# Authentication And Limits

## Authentication

Gateway keys use a recognizable prefix and cryptographically random secret.
Store only a display prefix and `HMAC-SHA256(server_pepper, raw_key)`; keep the
pepper outside SQLite and show the raw key only once. Disabled, expired, or
invalid keys never reach upstream. Cache compiled key policies in memory so the
hot path does not query SQLite.

## Effective Policy

Resolve one immutable policy per request from global defaults and key/model
rules. It may include exact/glob allow and deny model lists, request windows,
token windows, maximum concurrency, budget, token mode, and logging policy.
Deny takes precedence. Do not add regex without a concrete need.

## Request Limits

Request limits are amount plus duration; RPM is the ordinary `amount / 1m`
policy form. The in-memory implementation uses fixed windows whose boundaries
are aligned to integer multiples of each duration from the Unix epoch. Semantics
are tested with an injectable clock.
Consume request capacity before upstream work and normally do not refund it for
an upstream error. Return HTTP 429 and `Retry-After` when reset time is known.

## Concurrency

Concurrency is per key and unlimited unless configured. Reject immediately with
429 when no slot is available; hidden unbounded queues are not part of MVP.
Release the slot after success, upstream error, cancellation, timeout, forced
termination, or internal error.

## Lease

One idempotent request lease eventually owns concurrency and token/budget
reservations. `Commit` replaces reservations with actual usage. `Abort` releases
or conservatively reconciles them according to policy. Either operation releases
the concurrency slot exactly once.
