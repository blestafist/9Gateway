# Current Work

Current milestone: admin read API, CLI, readiness/metrics, packaging
(`T141`-`T160`) — complete.

Done: `T001`-`T160`.

Current: none; milestone complete.

Queued: `T161+` only, explicitly out of scope for this release.

The gateway now provides complete admin read API, CLI tool, health/metrics endpoints, graceful shutdown, security hardening, and production packaging.
It also provides transparent policy enforcement, token and budget accounting,
bounded safe request tracing, optional per-key body capture, structured
completion logging, persistent request history with independent retention, and
ordered lifecycle shutdown. Detailed telemetry is best effort and droppable;
transport and critical accounting remain independent of its queues and sinks.
Provider routing/translation, tool-call execution/validation, Redis,
PostgreSQL, Web UI, and T161+ follow-on work remain out of scope.

Review fixes: history shutdown now forms a SQLite-close completion barrier,
body captures transfer immutable ownership without defensive re-cloning,
retention passes are capped at 1000 rows while T140 verifies log/persistence
scalars, policy bodies, and restart retention. Storage review fixes for T136-T139:
lifecycle timestamp ordering constraints, indexed retention by completion time,
and an enforced schema/config body-size compatibility constant (schema version 9).

T155 added a minimal non-root Docker image with static gateway/gwctl binaries,
readiness healthcheck, and persistent `/data` defaults. T152 hardened semantic
admin IDs/cursors, upstream URL startup validation, structured error logging,
and early request-line limits; T154 audited control-plane secret redaction and
hardened credential-bearing error/header surfaces. T156-T160 completed Compose,
startup validation, version metadata, integration coverage, documentation, and
release-candidate preparation. Docker-daemon runtime checks remain environment
dependent, and known T157/T158 config test failures mean this candidate is not
test-green yet. No Web UI, provider routing, retries, Redis, or PostgreSQL.
