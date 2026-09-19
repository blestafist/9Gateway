# 9Gateway

9Gateway is a small OpenAI-compatible policy and accounting gateway in front of
9router:

```text
Client -> Gateway -> 9router -> Provider
```

The gateway authenticates client keys, enforces configured limits, records
bounded usage and request history, and transparently proxies `/v1/*` traffic.
Provider routing and protocol translation remain responsibilities of 9router.

## Quick start with Docker Compose

This is the fastest complete local walkthrough. It starts the gateway, the
repository's OpenAI-compatible mock upstream, and SQLite-backed request
history. Compose uses named volumes, so the gateway's non-root UID 65532 can
write `/data` without host ownership changes.

### Prerequisites

- Docker Engine or Docker Desktop with the Compose v2 plugin (`docker compose`)
- `curl`

The commands below use POSIX `sh` syntax and work in Ubuntu 22.04/24.04 shells
and macOS shells. The legacy `docker-compose` command is not used in this
guide; use a current Compose v2 installation.

### Start the example deployment

From the repository root:

```sh
cp .env.example .env
cp config.example.yaml config.yaml
```

Replace the three local-only values in `.env` before sharing the deployment.
The example configuration refers to those values as `${UPSTREAM_API_KEY}`,
`${AUTH_PEPPER}`, and `${ADMIN_CREDENTIAL}`. Start the services:

```sh
docker compose up --build -d
docker compose ps
curl -fsS http://localhost:8080/ready
```

`gateway` is published at `http://localhost:8080`; the mock upstream is only
available inside the Compose network at `http://mock-upstream:8081`.

### Create the first gateway key

The Compose service supplies `GWCTL_ADMIN_CREDENTIAL` from
`ADMIN_CREDENTIAL`, so no credential needs to be copied into the command. The
raw gateway key is printed only once; save it immediately:

```sh
docker compose exec -T gateway gwctl keys create local-demo
```

The output contains a line such as `Key: ...`. Set that value in `API_KEY` in
the shell for the next commands:

```sh
API_KEY='paste-the-Key-value-here'
```

### Send the first proxied request

The mock upstream accepts this OpenAI-compatible request:

```sh
curl -fsS http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-model","messages":[{"role":"user","content":"Say hello"}]}'
```

Streaming is transparent when requested:

```sh
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-model","stream":true,"messages":[{"role":"user","content":"Say hello"}]}'
```

### View request history

The CLI reads the authenticated admin API; it does not access SQLite directly:

```sh
docker compose exec -T gateway gwctl requests list --limit 10
docker compose exec -T gateway gwctl keys list
```

History persistence is asynchronous. If the first listing races the completion
worker, run the command again after a moment.

For Prometheus and the optional local Prometheus UI, start the observability
profile:

```sh
docker compose --profile observability up -d
```

Prometheus is then available at <http://localhost:9090> and scrapes
`http://gateway:8080/metrics` every 15 seconds.

### Open the Web UI

The production console is served by the gateway at <http://localhost:8080/ui/>; no
Node.js or frontend tooling is required on an operator workstation. Log in with
the `ADMIN_CREDENTIAL` value, then use **API Keys** to create the first gateway
key. The console can then send a request through the same `/v1/*` proxy, and the
Overview, Usage, Requests, and Request Details screens show the resulting
accounting and history. Use **Log out** when finished; the session is an in-memory,
opaque `HttpOnly`, `SameSite=Strict` cookie scoped to `/admin`, with idle and
absolute expiry and no local-storage credential persistence.

For a complete browser-session walkthrough, see the [deployment guide](docs/operations/deployment.md).

Stop the deployment while retaining data, or remove its named volumes:

```sh
docker compose down
docker compose down -v
```

## Installation from Go source

Binary installation requires Go 1.23 or newer. Build both binaries from the
repository root:

```sh
go build -o gateway ./cmd/gateway
go build -o gwctl ./cmd/gwctl
```

The gateway requires a YAML file and an upstream 9router URL. Copy
`config.example.yaml` and set `upstream_base_url` to the 9router address that
is reachable from the host (the Compose-only `mock-upstream` hostname will not
work for a host process):

```sh
cp config.example.yaml config.yaml
export UPSTREAM_API_KEY='your-9router-api-key'
export AUTH_PEPPER='a-long-random-secret'
export ADMIN_CREDENTIAL='a-different-admin-secret'
./gateway --config config.yaml
```

In another shell, use the locally built CLI. It defaults to
`http://localhost:8080`:

```sh
export GWCTL_ADMIN_CREDENTIAL="$ADMIN_CREDENTIAL"
./gwctl ping
created_key="$(./gwctl keys create local-demo)"
printf '%s\n' "$created_key"
export API_KEY="$(printf '%s\n' "$created_key" | awk -F': ' '$1 == "Key" {print $2; exit}')"
test -n "$API_KEY"
```

The raw key is printed only once; the commands above extract it and export it as
`API_KEY` for the same `curl` request shown above. See
[Deployment](docs/operations/deployment.md) for background binary, Docker,
volume, shutdown, and build-metadata guidance.

## Configuration

Configuration is strict YAML. Unknown fields, malformed values, missing
required secrets, unsafe upstream URLs, and invalid SQLite paths fail before
the listener opens. `auth_pepper` and `admin_credential` must be exact
environment references; `upstream_api_key` may be literal or an environment
reference. See the complete [configuration reference](docs/operations/configuration.md)
and the checked-in [example configuration](config.example.yaml).

## HTTP endpoints

- `GET /ui/` serves the embedded Web UI (including client-side routes).
- `GET /health` is an unauthenticated liveness response.
- `GET /ready` is an unauthenticated readiness response with SQLite, schema,
  telemetry, upstream-URL, and lifecycle checks.
- `GET /metrics` is an unauthenticated Prometheus text endpoint.
- `/v1/*` is the authenticated transparent proxy surface.
- `/admin/v1/*` is the admin API, authenticated with the admin credential.

See the [admin API reference](docs/operations/admin-api.md),
[CLI reference](docs/operations/cli.md), and [deployment guide](docs/operations/deployment.md)
for exact request, response, and operational details.

## History

The v0.1.0-rc1 candidate now includes the completed T161-T180 Web UI milestone:
admin read and policy APIs, the embedded operator console, `gwctl`, readiness and
metrics, graceful shutdown, security hardening, SQLite request history, and
Docker/Compose packaging. See [CHANGELOG.md](CHANGELOG.md), the [deployment
guide](docs/operations/deployment.md), and the [release checklist](docs/release-checklist.md)
for scope and verification status.
