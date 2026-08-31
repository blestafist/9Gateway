# Active Tasks

This file contains only the active queue. Read `AGENTS.md`, `CURRENT.md`, and the
single task being implemented. Do not load the whole cold backlog. When fewer
than five tasks remain, promote the next coherent batch from
`docs/tasks/backlog-full.md` and link only relevant architecture documents.

## Repository Skeleton

### T001 - Create Go module

Goal: create `go.mod` and a minimal `cmd/gateway/main.go` that exits normally.

Acceptance: `go build ./...` passes; no HTTP behavior is added.

Reference: `docs/architecture/repository.md#initial-structure`.

### T002 - Add initial directories

Goal: add `internal/config`, `internal/httpserver`, `internal/proxy`,
`internal/transport`, and `internal/streaming` as they become useful.

Acceptance: the project builds; no empty interfaces or boilerplate-only Go files.

Reference: `docs/architecture/repository.md#initial-structure`.

### T003 - Add minimal Config type

Goal: define listen address, upstream base URL, and upstream API key in
`internal/config/config.go`.

Acceptance: construction and validation are unit tested; no storage, limits,
pricing, or logging configuration.

Reference: `docs/architecture/operations.md#configuration`.

### T004 - Load YAML config

Goal: load configuration from the path supplied by `--config`.

Acceptance: valid YAML loads; malformed YAML and a missing upstream URL return
clear errors.

Reference: `docs/architecture/operations.md#configuration`.

### T005 - Resolve upstream key from environment

Goal: allow the upstream API key to reference an environment variable without
adding a general template engine.

Acceptance: the secret resolves from the environment; a missing variable causes
a startup error.

Reference: `docs/architecture/operations.md#configuration`.

## Minimal HTTP Server

### T006 - Start HTTP server

Goal: serve one handler on the configured address.

Acceptance: the server starts and accepts an HTTP request; graceful shutdown is
out of scope.

Reference: `docs/architecture/operations.md#server-lifecycle`.

### T007 - Add health endpoint

Goal: implement `GET /health` as a process-liveness endpoint.

Acceptance: an integration test receives HTTP 200; no upstream check occurs.

Reference: `docs/architecture/operations.md#health`.

### T008 - Add request ID

Goal: create a unique ID for every request and return it in
`X-Gateway-Request-ID`.

Acceptance: two requests receive distinct IDs.

Reference: `docs/architecture/observability.md#request-trace`.

### T009 - Add structured completion log

Goal: log request ID, method, path, status, and duration once per completed
request.

Acceptance: one request produces one completion record; body and Authorization
are absent.

Reference: `docs/architecture/observability.md#logging`.

## Raw Reverse Proxy

### T010 - Create reusable upstream client

Goal: create one long-lived `http.Client`/`http.Transport` at startup.

Acceptance: requests do not construct their own client.

Reference: `docs/architecture/transport.md#upstream-client`.

### T011 - Proxy method, path, and query

Goal: forward any `/v1/*` request to the configured upstream with the same
method, path, and query parameters.

Acceptance: a mock upstream observes identical values.

Reference: `docs/architecture/transport.md#generic-passthrough`.

### T012 - Proxy request body

Goal: stream the request body upstream without parsing it.

Acceptance: mock upstream receives byte-identical content.

Reference: `docs/architecture/transport.md#request-body`.

### T013 - Rewrite Authorization

Goal: replace client Authorization with the configured 9router credential.

Acceptance: upstream receives only the upstream key.

Reference: `docs/architecture/transport.md#headers`.

### T014 - Copy end-to-end request headers

Goal: preserve end-to-end headers and remove hop-by-hop headers.

Acceptance: `Content-Type` is preserved and `Connection` is not forwarded.

Reference: `docs/architecture/transport.md#headers`.

### T015 - Preserve response status

Goal: return the upstream status unchanged.

Acceptance: upstream 418 produces downstream 418.

Reference: `docs/architecture/transport.md#generic-passthrough`.

### T016 - Copy end-to-end response headers

Goal: preserve upstream end-to-end response headers and remove hop-by-hop ones.

Acceptance: a custom upstream header reaches the client.

Reference: `docs/architecture/transport.md#headers`.

### T017 - Proxy ordinary response body

Goal: pass a non-stream response body without JSON unmarshalling or
reserialization.

Acceptance: JSON bytes remain identical.

Reference: `docs/architecture/transport.md#generic-passthrough`.

### T018 - Support unknown `/v1/*`

Goal: proxy unknown OpenAI-compatible endpoints without endpoint registration.

Acceptance: `POST /v1/something-unknown` reaches the mock upstream unchanged.

Reference: `docs/architecture/transport.md#generic-passthrough`.

## Cancellation

### T019 - Bind upstream to client context

Goal: create the upstream request with the incoming request context.

Acceptance: client cancellation cancels the context observed by upstream.

Reference: `docs/architecture/transport.md#cancellation`.

### T020 - Test client cancellation

Goal: add an HTTP integration test whose upstream waits for cancellation.

Acceptance: cancelling the client context is observed upstream without a long
timeout.

Reference: `docs/architecture/testing.md#transport-integration`.
