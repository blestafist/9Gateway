# Current Work

Current milestone: admin read API, CLI, readiness/metrics, packaging
(`T141`-`T160`).

Done: `T001`-`T157`.

Current: `T158`.

Queued: `T159`-`T160`.

The gateway now provides transparent policy enforcement, token and budget
accounting, bounded safe request tracing, optional per-key body capture,
structured completion logging, persistent request history with independent
retention, ordered lifecycle shutdown, and a thin `gwctl` admin API client
foundation. Detailed telemetry is best effort and droppable; transport and
critical accounting remain independent of its queues and sinks. Metrics and
request-history admin APIs, provider routing/translation, tool-call
execution/validation, Redis, PostgreSQL, and Web UI remain out of scope.

Review fixes: history shutdown now forms a SQLite-close completion barrier,
body captures transfer immutable ownership without defensive re-cloning, and
retention passes are capped at 1000 rows while T140 verifies log/persistence
scalars, policy bodies, and restart retention. Storage review fixes for T136-T139: lifecycle timestamp ordering constraints, indexed retention by completion time, and an enforced schema/config body-size compatibility constant (schema version 9).

T155 added a minimal non-root Docker image with static gateway/gwctl binaries,
readiness healthcheck, and persistent `/data` defaults. T152 hardened semantic
admin IDs/cursors, upstream URL startup validation, structured error logging, and
early request-line limits; T154 audited control-plane secret redaction and
hardened credential-bearing error/header surfaces. The remaining work starts
with compose and follow-on deployment work. No Web UI, provider routing,
retries, Redis, or PostgreSQL.
