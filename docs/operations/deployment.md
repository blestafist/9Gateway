# Deployment guide

9Gateway is one static Go gateway binary, one YAML file, and one SQLite
database. It does not require Redis, PostgreSQL, a reverse proxy, or a
companion service; 9router remains the upstream.

## Docker image

`Dockerfile` deterministically builds the Web UI before compiling the gateway.
The frontend stage pins Node.js `22.14.0` and npm `10.9.2`, installs from the
lockfile with `npm ci`, disables production source maps, and embeds only the
hashed `web/dist` assets into the Go binary. BuildKit's npm cache mount speeds
repeat builds without copying npm cache, Node modules, Playwright, or frontend
source into the final image. The final image contains no Node runtime or
writable web asset directory.

`Dockerfile` is a multi-stage build. It compiles static `gateway` and `gwctl`
with `CGO_ENABLED=0`; the runtime is
`gcr.io/distroless/static-debian12:nonroot`. The final process is UID/GID
65532, includes CA certificates and timezone data, has no shell or package
manager, exposes 8080, owns `/data`, and contains `/gateway` and `/gwctl`.

Build and run with a named volume:

```sh
docker build -t 9gateway:local .
docker volume create 9gateway-data
docker run --rm --name 9gateway \
  --mount source=9gateway-data,target=/data \
  --mount type=bind,src="$PWD/config.yaml",dst=/etc/gateway/config.yaml,readonly \
  --env UPSTREAM_API_KEY="$UPSTREAM_API_KEY" \
  --env AUTH_PEPPER="$AUTH_PEPPER" \
  --env ADMIN_CREDENTIAL="$ADMIN_CREDENTIAL" \
  -p 8080:8080 9gateway:local
```

Open <http://localhost:8080/ui/> after the container is ready. The browser
session is an opaque, in-memory, `HttpOnly`, `SameSite=Strict` cookie scoped to
`/admin`; it rotates at login, expires on idle/absolute limits or process
restart, and is revoked by logout. The UI requires no Node.js installation.
Use **API Keys** to create a gateway key, send a request to the configured
upstream, then inspect Usage, Requests, and Request Details before logging out.
The UI is served under `/ui/`, while the backend surfaces it calls are
`/admin/ui/v1/session`, `/admin/v1/overview`, `/admin/v1/usage/*`,
`/admin/v1/keys/*`, `/admin/v1/requests/*`, and `/admin/v1/system`; gateway
traffic remains on `/v1/*`.

The entrypoint is `/gateway` and its default command is
`--config /etc/gateway/config.yaml`. `/gwctl` is on `PATH`:

```sh
docker exec 9gateway gwctl --admin-credential "$ADMIN_CREDENTIAL" keys list
```

The image healthcheck runs `/gateway healthcheck`, probing
`http://127.0.0.1:8080/ready`. Set `GATEWAY_HEALTHCHECK_ADDRESS` or pass
`/gateway healthcheck --address HOST:PORT`/`--address URL` for another
listener; the probe always uses `/ready`.

Release metadata is passed to binaries and OCI labels:

```sh
docker build -t 9gateway:v0.1.0-rc1 \
  --build-arg VERSION=v0.1.0-rc1 \
  --build-arg COMMIT_SHA="$(git rev-parse HEAD)" \
  --build-arg BUILD_DATE=2026-09-14T00:00:00Z .
```

BuildKit supports `TARGETOS` and `TARGETARCH`, for example
`docker buildx build --platform linux/amd64,linux/arm64 ...`. A multi-platform
push needs a registry and is not part of the local Compose flow.

## Compose

`docker-compose.yml` defines `gateway` (image `9gateway:local`, port
`8080:8080`, named `9gateway-data` volume and read-only config mount),
`mock-upstream` (built from `tests/mock-upstream`, internal port 8081, health
dependency), and optional `prometheus` under the `observability` profile (port
9090, named `9gateway-prometheus` volume).

```sh
cp .env.example .env
cp config.example.yaml config.yaml
```

Required variables are `UPSTREAM_API_KEY`, `ADMIN_CREDENTIAL`, and
`AUTH_PEPPER`. `GATEWAY_CONFIG` optionally changes the host config path (default
`./config.yaml`). Inside `gateway`, `GWCTL_ADMIN_CREDENTIAL` is set from
`ADMIN_CREDENTIAL`; the mock receives only `UPSTREAM_API_KEY`.

Check interpolation and YAML statically with `docker compose config` (with
`.env`, or equivalent exported variables, present). A daemon is not needed for
that command, but is needed to build or run services.

## Development and release builds

For local UI development, run the gateway and Vite together. The Vite server
proxies gateway routes to port 8080 and serves the UI at `/ui/`:

```sh
go run ./cmd/gateway --config config.yaml
npm --prefix web ci
npm --prefix web run dev
```

For a production-like local build, use the pinned toolchain (`node --version`
must be `v22.14.0` and `npm --version` must be `10.9.2`), then run:

```sh
npm --prefix web ci
npm --prefix web run lint
npm --prefix web run test
npm --prefix web run build
npm --prefix web run budget:check
npm --prefix web run report:dependencies
go fmt ./...
go test ./...
go build ./...
```

The release Docker build repeats the frontend build in an isolated stage, so a
stale or locally generated `web/dist` cannot replace the lockfile build. Build
with metadata using the `VERSION`, `COMMIT_SHA`, and `BUILD_DATE` arguments shown
above. A clean checkout therefore needs Docker and does not need frontend
artifacts committed to the repository.

## Binary deployment

Go 1.23 or newer is required:

```sh
go build -o gateway ./cmd/gateway
```

Set `upstream_base_url` to an actual reachable 9router URL (not the Compose
hostname), export the three required secrets, and start:

```sh
export UPSTREAM_API_KEY='...'
export AUTH_PEPPER='...'
export ADMIN_CREDENTIAL='...'
./gateway --config /path/to/config.yaml
```

`--config` is the only gateway config selector. `gateway --version` and
`gwctl version` print version, short commit, build date, Go version, and
OS/arch. Defaults without build flags are `dev`/`unknown`/`unknown`.

## Volumes and permissions

The default database is `/data/gateway.db`. Named volumes are recommended and
preserve UID 65532 ownership. A Linux bind mount must be writable by that UID:

```sh
install -d -m 0750 -o 65532 -g 65532 data
```

On macOS Docker Desktop, prefer the named volume; for a bind mount, share the
directory with Docker Desktop and verify writes. The config mount is read-only
and only needs to be readable by UID 65532. Never put secrets in the image or
commit `.env`.

## Health, metrics, and shutdown

- `/health` returns 200 when the HTTP process is serving.
- `/ready` is unauthenticated and returns 200 only when SQLite, schema,
  telemetry acceptance, upstream URL, and lifecycle checks pass; otherwise it
  returns 503. Checks have a two-second deadline and do not ping 9router.
- `/metrics` is unauthenticated Prometheus text with request/error/upstream/
  telemetry counters, active/queue gauges, and request/upstream/TTFB
  histograms. Labels are fixed vocabularies and exclude keys, credentials,
  model names, and body content.

The checked-in `prometheus.yml` scrapes `gateway:8080/metrics` every 15 seconds.
Startup validates config and secrets, opens/migrates SQLite, restores limiter
state, starts workers, and only then listens. SIGTERM/SIGINT makes readiness
fail, stops new work, waits for active handlers up to
`shutdown_timeout_seconds` (default 30, maximum 600), completes accounting,
drains bounded queues, and closes SQLite last. A second interrupt can force
termination. Container runtimes should allow the configured deadline.

## Browser support, HTTPS, accessibility, and troubleshooting

The tested browser matrix is current Chromium desktop (dark and light),
Chromium mobile emulation, and Firefox. WebKit is a separate optional check and
requires its host libraries; see [UI verification](../ui/budgets-and-verification.md).
The console follows the documented WCAG 2.2 AA keyboard, focus, contrast, and
responsive behavior; see [accessibility notes](../ui/accessibility.md).

For internet-facing use, terminate TLS at a trusted reverse proxy and forward
only the required headers/configured trusted proxy addresses. Set
`GATEWAY_TRUSTED_PROXIES` so the gateway can safely recognize direct TLS or the
proxy's HTTPS connection when issuing the `Secure` browser-session cookie. Do
not expose the admin UI over plaintext HTTP outside a local development network;
never cache `/ui/` HTML or admin responses at a shared proxy. Hashed assets under
`/ui/assets/` are immutable-cacheable; `index.html` and SPA fallbacks are
`no-cache`, preventing stale asset manifests after a release.

If `/ui/` is blank, verify the gateway was built with the web stage and inspect
that `/ui/` returns HTML while `/ui/assets/*` returns the referenced files. If a
browser reports a stale chunk after deployment, hard-refresh only after checking
that the proxy is not caching `index.html`. A failed login usually indicates an
incorrect `ADMIN_CREDENTIAL`, an expired session, or missing CSRF handling; the
session cookie is intentionally inaccessible to JavaScript. If Docker cannot
build, check that a Docker daemon and BuildKit are available; frontend-only
checks can still run with the pinned local Node/npm versions.

## Platform notes

Build and shell examples use standard Go, curl, and POSIX shell features on
Ubuntu and macOS. Docker image execution is Linux-container based; Docker
Desktop supplies that environment on macOS. The ownership command is a Linux
bind-mount example, not a requirement for macOS named volumes.
