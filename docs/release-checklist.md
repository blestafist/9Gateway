# v0.1.0-rc1 release checklist

This records the T160 audit without claiming checks requiring a Docker daemon.
The candidate is prepared but not tagged.

| Area | Result | Evidence |
| --- | --- | --- |
| T141-T145 admin APIs | Implemented and covered | `internal/httpserver/admin.go`, storage repositories, focused HTTP tests |
| T146-T148 `gwctl` | Implemented and covered | `internal/gwctl/`, package tests |
| T149 readiness | Implemented and covered | `internal/httpserver/readiness.go`, readiness tests |
| T150 metrics | Implemented and covered | `internal/httpserver/metrics.go`, metrics tests |
| T151 lifecycle | Implemented and covered | `cmd/gateway/main.go`, shutdown tests |
| T152-T154 hardening | Implemented and covered | `internal/security/`, limits, redaction, integration tests |
| T155 Docker image | Static source checks only | `Dockerfile`, `cmd/gateway/dockerfile_test.go`; daemon build/run/scan unavailable |
| T156 Compose | Static YAML/interpolation checks only | `docker-compose.yml`, `deployment_test.go`; daemon runtime unavailable |
| T157 configuration | Intended behavior documented; known test failures remain | `internal/config/`; blockers below |
| T158 version metadata | Implemented and covered | `internal/version/`, version tests |
| T159 integration | Implemented; full run is diagnostic until known failures are fixed | `internal/integration/gateway_test.go` |
| T160 release preparation | Completed by this change | README, operations docs, changelog, CURRENT.md |

## Verification status

Run and report `go fmt ./...`, `go build ./...`, `go test ./...`,
`go test -race ./...` where practical, `docker compose config` with `.env`,
Markdown link/file checks, `git diff --check`, and the critical-path
TODO/FIXME audit. Image build/inspection, container startup, Compose health,
Prometheus scraping, and Docker scan/Trivy results require a Docker daemon and
must not be represented as successful without one.

## Known blocker

The repository has three known `internal/config` test failures from T157/T158
behavior (`TestConfigValidate/admin_credential_must_be_distinct`,
`TestLoadFailsWhenUpstreamAPIKeyEnvironmentVariableIsMissing`, and
`TestLoadSecretEnvironmentReferences/empty_pepper`). T160 deliberately does not
repair them. The intended behavior is documented in
[configuration.md](operations/configuration.md); the candidate is **not green**
until those pre-existing failures are resolved.

## Scope boundary

T161+ is explicitly out of scope. Do not add Web UI, provider routing or
translation, retries, Redis, PostgreSQL, load testing, or other follow-on work.
