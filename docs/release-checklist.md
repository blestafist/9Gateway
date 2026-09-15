# v0.1.0-rc1 release checklist

This records T160 completion and release-candidate readiness. The Go suite and
build are green. Checks requiring a Docker daemon were not run in this
environment and are explicitly not represented as passed. The candidate is
prepared but not tagged.

| Area | Result | Evidence |
| --- | --- | --- |
| T141-T145 admin APIs | Implemented and covered | `internal/httpserver/admin.go`, storage repositories, focused HTTP tests |
| T146-T148 `gwctl` | Implemented and covered | `internal/gwctl/`, package tests |
| T149 readiness | Implemented and covered | `internal/httpserver/readiness.go`, readiness tests |
| T150 metrics | Implemented and covered | `internal/httpserver/metrics.go`, metrics tests |
| T151 lifecycle | Implemented and covered | `cmd/gateway/main.go`, shutdown tests |
| T152-T154 hardening | Implemented and covered | `internal/security/`, limits, redaction, integration tests |
| T155 Docker image | Source checks implemented and covered | `Dockerfile`, `cmd/gateway/dockerfile_test.go`; image build/runtime, healthcheck, image-size, and vulnerability scan not run (Docker daemon unavailable) |
| T156 Compose | YAML/interpolation checks implemented and covered | `docker-compose.yml`, `deployment_test.go`; Compose runtime and Prometheus scrape not run (Docker daemon unavailable) |
| T157 configuration | Implemented and covered; suite green | `internal/config/`, configuration tests |
| T158 version metadata | Implemented and covered | `internal/version/`, version tests |
| T159 integration | Implemented and covered; full suite green | `internal/integration/gateway_test.go` |
| T160 release preparation | Complete; release-candidate documentation is consistent | README, operations docs, changelog, CURRENT.md |

## Verification status

The completed local verification is `go fmt ./...`, `go test ./...`,
`go build ./...`, and `git diff --check`. The Compose config command was also
requested, but this environment has no `docker compose` plugin, so
`docker compose --env-file .env.example config` could not run; this is an
environment limitation, not a Compose configuration failure. Markdown links
and file references in the release docs point to checked-in files.

The Docker daemon-dependent checks were not run: image build/inspection,
container startup/runtime, gateway healthcheck, Compose health, Prometheus
scraping, image-size measurement, and vulnerability scanning (Docker
scan/Trivy). None are claimed as passed.

## Scope boundary

T161+ is explicitly out of scope. Do not add Web UI, provider routing or
translation, retries, Redis, PostgreSQL, load testing, or other follow-on work.
