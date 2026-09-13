# Current Work

Current milestone: admin read API, CLI, readiness/metrics, packaging
(`T141`-`T160`).

Done: `T001`-`T140`.

Current: none (awaiting agent assignment).

Queued: `T141`-`T160`.

The gateway now provides transparent policy enforcement, token and budget
accounting, bounded safe request tracing, optional per-key body capture,
structured completion logging, persistent request history with independent
retention, and ordered lifecycle shutdown. Detailed telemetry is best effort
and droppable; transport and critical accounting remain independent of its
queues and sinks. `/metrics`, `/ready`, CLI, request-history admin APIs,
provider routing/translation, tool-call execution/validation, Redis,
PostgreSQL, and Web UI remain out of scope.

Review fixes: history shutdown now forms a SQLite-close completion barrier,
body captures transfer immutable ownership without defensive re-cloning, and
retention passes are capped at 1000 rows while T140 verifies log/persistence
scalars, policy bodies, and restart retention. Storage review fixes for T136-T139: lifecycle timestamp ordering constraints, indexed retention by completion time, and an enforced schema/config body-size compatibility constant (schema version 9).

The next milestone makes stored history and key policy readable and
operable: paginated admin read endpoints for keys/requests/bodies, a thin
`gwctl` CLI over that API, `/ready`, `/metrics`, explicit ordered graceful
shutdown, path/body-size hardening, and a minimal Docker image/compose. No
Web UI, provider routing, retries, Redis, or PostgreSQL.
