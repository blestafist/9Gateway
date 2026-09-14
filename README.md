# 9Gateway local compose deployment

This repository includes a small, self-contained deployment for local testing
and small installations: the gateway, an OpenAI-compatible mock upstream, and
optional Prometheus metrics. It uses a named SQLite volume so the image's
non-root UID 65532 can write `/data` safely.

## Quick start

```sh
cp .env.example .env
cp config.example.yaml config.yaml
# Edit .env and replace the example values with local, non-production secrets.
docker compose up --build -d
docker compose ps
curl http://localhost:8080/ready
```

The compose file uses the modern `docker compose` command. On systems that
still provide the legacy v1 binary, `docker-compose` is equivalent for these
commands (except that the `observability` profile requires a recent Compose
implementation).

Create the first gateway API key from the container. The command prints the
credential only at creation time, so save it immediately:

```sh
ADMIN_CREDENTIAL="$(grep '^ADMIN_CREDENTIAL=' .env | cut -d= -f2-)"
API_KEY="$(docker compose exec -T -e GWCTL_ADMIN_CREDENTIAL="${ADMIN_CREDENTIAL}" gateway gwctl keys create local-demo --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')"
printf 'Gateway API key: %s\n' "$API_KEY"
```

Use that key against the gateway. The mock accepts both regular JSON and
transparent SSE requests:

```sh
curl -fsS http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-model","messages":[{"role":"user","content":"Say hello"}]}'

curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-model","stream":true,"messages":[{"role":"user","content":"Say hello"}]}'
```

Inspect persisted request history with the admin CLI (the container provides
the exact current syntax):

```sh
docker compose exec -e GWCTL_ADMIN_CREDENTIAL="${ADMIN_CREDENTIAL}" gateway gwctl requests list --limit 10
docker compose exec -e GWCTL_ADMIN_CREDENTIAL="${ADMIN_CREDENTIAL}" gateway gwctl keys list
```

To enable Prometheus, start the optional profile and open
<http://localhost:9090>:

```sh
docker compose --profile observability up -d
```

Prometheus scrapes `http://gateway:8080/metrics` every 15 seconds. Stop the
deployment while retaining the named volumes with `docker compose down`, or
remove the SQLite and Prometheus data with `docker compose down -v`.
