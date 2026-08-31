# Architecture Index

Architecture is reference material, not mandatory context. A task should link
only the document and section needed for that change.

- `repository.md`: package boundaries, dependencies, request orchestration.
- `transport.md`: transparent proxying, headers, response classification,
  cancellation, timeouts.
- `streaming.md`: SSE passthrough, parsing, aggregation, termination.
- `policy.md`: authentication, policy resolution, rate and concurrency limits.
- `accounting.md`: token estimation, reservations, usage, pricing, budgets.
- `storage.md`: SQLite schema, caches, persistence boundaries.
- `observability.md`: traces, body capture, telemetry, metrics and logging.
- `operations.md`: configuration, startup, health, admin, CLI and deployment.
- `testing.md`: mock upstream, regressions, security and performance tests.

`PLAN.md` remains the product source of truth. The original monolithic design is
preserved at `docs/archive/ARCHITECTURE-full.md` for details not yet promoted
into a subsystem document.
