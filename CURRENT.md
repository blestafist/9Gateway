# Current Work

Current milestone: complete (`T121`-`T140` observability).

Done: `T001`-`T140`.

Current: unset.

Queued: none.

The gateway now provides transparent policy enforcement, token and budget
accounting, bounded safe request tracing, optional per-key body capture,
structured completion logging, persistent request history with independent
retention, and ordered lifecycle shutdown. Detailed telemetry is best effort
and droppable; transport and critical accounting remain independent of its
queues and sinks. `/metrics`, `/ready`, CLI, request-history admin APIs,
provider routing/translation, tool-call execution/validation, Redis,
PostgreSQL, and Web UI remain out of scope.
