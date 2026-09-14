# T141-T160 Review Handoff

## Status

- Tasks `T141` through `T160` are implemented and committed.
- `CURRENT.md` currently marks the milestone complete.
- A full independent review of `T141`-`T160` was completed after `T160`.
- Review fixes below have not yet been implemented.
- Do not modify unrelated untracked files `TASKS_old.md` and `gwctl`.
- Docker is installed, but the daemon is unavailable in this environment.

Recent task commits:

- `88846ff` - `T156 add local docker compose deployment`
- `c9af3d4` - `T157 validate configuration before startup`
- `18ca223` - `T158 add version and build metadata`
- `a53d72d` - `T159 add comprehensive integration suite`
- `43ab6dc` - `T160 complete milestone documentation and release prep`

## Required Fixes

### Configuration and release state

- Update three stale assertions in `internal/config/config_test.go` and
  `internal/config/load_test.go` to match T157's standardized safe error format.
  Runtime validation is correct; the stale tests make `go test ./...` and CI fail.
- Once the suite is green, remove the known-failure language from release docs and
  make the T160 completion/release-readiness statements consistent.
- In the binary quickstart in `README.md`, explicitly export the one-time raw key
  as `API_KEY` after `gwctl keys create`, as already done in the Compose walkthrough.

### Admin request pagination

- Bind request cursors to normalized `key_id`, `after`, and `before` filters and
  reject cursor/filter mismatches with HTTP 400. Current cursors can be reused
  with different filters and silently skip matching records.
- Clarify or fix retention behavior during pagination. The insertion-sequence fence
  prevents new inserts from entering a traversal, but retention can delete rows
  between pages. Prefer an explicit documented cursor-expired/retry contract unless
  a durable server-side snapshot is justified.

### Metrics

- Ensure ingress rejections such as oversized `Content-Length` and URI/query limits
  increment the primary `gateway_requests_total`, not only
  `gateway_rejected_requests_total`.
- Consider adding bounded `gateway_build_info` labels for scrapeable build metadata.
  T158's required `# gateway version ...` comment exists, but a comment is not
  queryable through PromQL.

### Transparent response transport

- Revisit non-SSE response limiting in `internal/httpserver/server.go`. It currently
  spools the complete upstream body before committing headers, which violates the
  transparent first-byte invariant for slow or streaming non-SSE responses. Stream
  immediately through a bounded reader; after downstream commit, terminate at the
  limit without forwarding bytes beyond it.
- Recheck the SSE event limiter against CR-only and mixed CR/LF delimiters across
  read boundaries. One reviewer reported a remaining CR-only regression despite
  earlier tests/fixes, so reproduce before changing code.

### T159 integration acceptance gaps

- Add a real budget-limited scenario; the current token/budget test only configures
  token windows.
- Correlate upstream-error, timeout, cancellation, and rejection history records and
  assert outcome, upstream-started state, status, usage/cost, and reconciliation.
- Saturate completion telemetry through live requests and a blocked sink instead of
  direct `logger.Enqueue` calls, then prove transport and critical accounting remain
  non-blocking/correct.
- Measure concurrent SSE duration from before `client.Do` through EOF, fail on read
  errors, and enforce the stated overhead/total-duration thresholds without the
  current compound allowance.
- Add goroutine leak checking with `go.uber.org/goleak` and narrow documented ignores.
- Define a meaningful `-coverpkg` package set and close the gap to the requested 80%
  happy-path coverage. Current measurements reported about 52.9% for httpserver and
  40.8% for all internal packages.
- Strengthen graceful-shutdown integration coverage with an in-flight request and
  verify drain, reconciliation, persistence, and production lifecycle coordination.

## Suggested Fix Order

1. Fix the three stale config tests and restore `go test ./...` to green.
2. Reproduce and fix transport/SSE findings, then run focused race tests.
3. Fix metrics and cursor/filter behavior with HTTP/storage regression tests.
4. Strengthen the T159 integration suite and add goleak/coverage enforcement.
5. Correct README/release status after all checks pass.
6. Run another independent review of the resulting diff.

## Required Verification

```text
go fmt ./...
go test ./...
go build ./...
go test -race ./...
go test -v ./internal/security -run TestSecretRedaction
go test -race ./internal/integration
docker compose --env-file .env.example config
git diff --check
```

Also run Docker image/Compose runtime, healthcheck, Prometheus scrape, image-size,
and vulnerability-scan checks when a Docker daemon is available. Do not claim those
checks passed until they actually run.
