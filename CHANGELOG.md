# Changelog

## v0.1.0-rc1 — release candidate

Candidate prepared from T141-T160. This task prepares the candidate and does
not create a git tag.

### Admin and inspection (T141-T145)

- Added paginated safe API-key listing and complete key detail reads.
- Added paginated request-history listing with key and completion-time filters.
- Added request details with nullable scalar fields and captured-body metadata.
- Added exact captured-body downloads with binary-safe responses and truncation
  metadata.
- Kept cursors opaque, authenticated, URL-safe, and stable during traversal;
  metadata paths exclude raw credentials and body data.

### CLI (T146-T148)

- Added standalone `gwctl` version and authenticated ping commands.
- Added `keys create/list/get` with human and JSON output and pagination.
- Added `requests list/get` filters, formatting, and exact body output to stdout
  or an atomic output file.
- Defined usage/API exit statuses 1 and 2 with sanitized diagnostics.

### Operations (T149-T151)

- Added unauthenticated `/ready` with bounded SQLite, schema, telemetry,
  upstream-URL, and lifecycle checks.
- Added Prometheus `/metrics` counters, gauges, and latency histograms with
  bounded labels.
- Added ordered SIGTERM/SIGINT shutdown, queue draining, accounting completion,
  readiness transition, and final SQLite close.

### Security and limits (T152-T154)

- Hardened identifiers, cursors, upstream URLs, request-line bounds, and
  structured error/log surfaces.
- Enforced 10 MiB request and 100 MiB non-stream response boundaries, bounded
  SSE events, and independent body-capture limits.
- Added secret-redaction coverage for logs, metrics, errors, and admin output.

### Packaging and verification (T155-T160)

- Added static non-root distroless Docker packaging for `gateway` and `gwctl`,
  `/data` persistence, readiness healthcheck, and build metadata.
- Added Compose deployment with mock upstream, required secret interpolation,
  named volumes, and optional Prometheus profile.
- Added strict startup config validation, environment-secret resolution,
  SQLite checks, pricing validation, and version/build metadata.
- Added live HTTP/SQLite integration coverage and completed the milestone
  documentation, operations references, and release checklist.

### Web UI milestone (T161-T180)

- Added the responsive authenticated operator console under `/ui/` for overview,
  usage, API keys, request history/details, body inspection, and diagnostics.
- Integrated deterministic Vite production builds into Docker and CI with pinned
  Node.js 22.14.0/npm 10.9.2, lockfile installs, npm cache mounts, and no
  production source maps or frontend tooling in the runtime image.
- Documented the UI quickstart, browser session/HTTPS requirements, supported
  browsers, accessibility expectations, immutable asset caching, stale-asset
  troubleshooting, and UI-visible admin APIs.
- Completed the T161-T180 acceptance audit. Docker daemon-dependent image,
  runtime, size, and vulnerability checks remain explicitly environment-blocked;
  WebKit remains host-library blocked as recorded in the UI verification matrix.

### Scope boundary

No provider routing/translation, Redis, PostgreSQL, load testing, or T181+
follow-on feature is included.
