# v0.1.0-rc1 release checklist

This records T180 completion and release-candidate readiness. The Go and web
suites, production asset build, budgets, accessibility, and feasible browser
projects are green. Checks requiring a Docker daemon or unavailable browser host
libraries were not run and are explicitly not represented as passed. The
candidate is prepared but not tagged.

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
| T161-T180 Web UI milestone | Complete; embedded deterministic production build, operator docs, approved desktop/mobile dark/light screenshots, and final acceptance audit | `Dockerfile`, `.github/workflows/test.yml`, `web/`, `docs/ui/login-desktop-{dark,light}.png`, `docs/ui/login-mobile-{dark,light}.png`, README, deployment guide, CURRENT.md |

## Verification status
The completed local verification includes `go fmt ./...`, `go test ./...`,
`go build ./...`, `go test -race ./...`, `git diff --check`, frontend lint,
Vitest (333 tests including axe), production build, bundle budget,
dependency-boundary report, npm audit review, and Playwright Chromium,
Chromium-Light, Mobile-Chromium, and Firefox projects (45 tests). Final assets
were visually reviewed at desktop/mobile dark/light viewports and checked for
horizontal overflow. Markdown links and file references in the release docs
point to checked-in files.

The focused `go test -race ./...` run reports the pre-existing
`TestT179_GatewayRSSGrowthUnderUIAndCachedAnalytics` RSS budget failure (about
61 MiB versus 32 MiB); the rest of the race suite passes. `npm audit
--audit-level=high` reports two moderate transitive Vitest advisories requiring
a breaking major upgrade, so no forced dependency upgrade was applied.
The Compose config command was requested, but this environment has no Docker
Compose plugin.

The Docker daemon-dependent checks were not run: image build/inspection,
container startup/runtime, gateway healthcheck, Compose health, Prometheus
scraping, image-size measurement, and vulnerability scanning (Docker
scan/Trivy). None are claimed as passed.

## Scope boundary

T161-T180 is the completed embedded Web UI milestone. Do not add provider
routing or translation, retries, Redis, PostgreSQL, load testing, or T181+
follow-on work.
