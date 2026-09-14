# Admin API reference

The admin API is served under `/admin/v1`. Every endpoint requires exactly one
`Authorization` header with value `Bearer <ADMIN_CREDENTIAL>`. The credential
is separate from gateway API keys. Missing, duplicated, or incorrect
credentials return HTTP 401. Metadata endpoints do not return raw keys,
digests, peppers, upstream credentials, policy JSON, request headers, or body
bytes.

JSON request bodies must use an `application/json` media type, are bounded to
16 KiB, reject unknown fields and duplicate JSON keys, and reject trailing JSON.
Errors use a safe envelope:

```json
{"error":{"message":"Incorrect API key provided.","type":"authentication_error","code":"invalid_api_key"}}
```

Messages and codes are fixed safe values; parser, SQL, URL, credential, and
storage details are not returned. Common admin codes are `invalid_api_key`,
`invalid_request`, `not_found`, `conflict`, and `gateway_internal_error`.

## `POST /admin/v1/keys`

Creates a key and returns the raw credential exactly once. Request:

```json
{"name":"local-demo","expires_at":"2030-01-02T03:04:05Z"}
```

`name` is required, non-blank, and at most 256 bytes. `expires_at` is optional
and, when present, must be RFC3339; it is normalized to UTC whole seconds.
Success is HTTP 201 and includes `id`, `name`, `prefix`, `enabled`,
`created_at`, `key`, and `policy`. `expires_at` is omitted without an expiry.
Invalid bodies return HTTP 400, oversized bodies return 413, and internal
creation failures return 500.

## `GET /admin/v1/keys`

Lists safe key metadata newest-first. `limit` defaults to 50 and has a maximum
of 500. `cursor` is an optional opaque, URL-safe authenticated cursor from
`next_cursor`.

```sh
curl -fsS 'http://localhost:8080/admin/v1/keys?limit=50' \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

The response is `{ "keys": [...], "next_cursor": "..." }`; the cursor is
omitted on the last page and an empty result has `"keys":[]`. Each item has
`id`, `name`, `display_prefix`, `enabled`, `created_at`, `updated_at`, nullable
`expires_at`, and `policy_summary` booleans `allow_models`, `deny_models`,
`log_request_body`, and `log_response_body`. Invalid query values return 400.

## `GET /admin/v1/keys/:id`

Returns one key's complete effective policy. `:id` uses the alphanumeric,
hyphen, underscore identifier grammar. Invalid IDs return 400; unknown IDs 404.

```sh
curl -fsS http://localhost:8080/admin/v1/keys/key-0123456789abcdef \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

The response has metadata plus `policy` fields `allowed_models`,
`denied_models`, `request_windows`, `token_windows`, `token_mode`,
`max_concurrent_requests`, `budget_limits`, `log_request_body`, and
`log_response_body`. Durations are integer seconds and budgets integer micros.
Stored invalid policy data is an internal error, not a 404.

## `PUT /admin/v1/keys/:id/policy`

Atomically replaces enabled state and policy:

```json
{"enabled":true,"policy":{"allowed_models":["mock-*"],"max_concurrent_requests":2}}
```

Both fields are required; `policy` must be valid policy JSON. Success is HTTP
200 and returns updated metadata. Invalid bodies return 400, missing keys 404,
and active token/budget work that makes replacement unsafe returns 409.

## `GET /admin/v1/requests`

Lists completed request metadata newest-first. Query parameters are `limit`
(default 50, max 500), opaque `cursor`, optional `key_id`, and optional
RFC3339 inclusive `after`/`before` completion-time bounds. `after` cannot be
later than `before`; unknown key IDs return no rows.

```sh
curl -fsS 'http://localhost:8080/admin/v1/requests?limit=10&key_id=key-demo' \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

Each `requests` item contains `request_id`, nullable key ID/name/model,
method/path/route, requested/upstream/delivered modes, nullable statuses,
terminal outcome, upstream-started, nullable error code, byte/token/cost
scalars, timestamps, and microsecond latencies. Null remains JSON `null`; no
body BLOBs are selected. Invalid filters/cursors return 400.

## `GET /admin/v1/requests/:id`

Returns the same scalar fields plus `has_bodies`, an array containing zero or
more of `client_request`, `upstream_request`, and `response`. IDs must be at
least 32 characters in the identifier grammar. Unknown IDs return 404 and
malformed IDs 400.

```sh
curl -fsS http://localhost:8080/admin/v1/requests/0123456789abcdef0123456789abcdef \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

## `GET /admin/v1/requests/:id/bodies/:kind`

Downloads exactly one captured body; `kind` is `client_request`,
`upstream_request`, or `response`. HTTP 200 has
`Content-Type: application/octet-stream`, `X-Original-Size: <integer>`, and
`X-Truncated: true|false`, followed by exact stored bytes. It does not decode,
redact, format, or decompress. Missing rows return 404, malformed IDs/kinds
400, and inconsistent unsafe stored content 500.

```sh
curl -fsS -D body.headers -o response.body \
  http://localhost:8080/admin/v1/requests/0123456789abcdef0123456789abcdef/bodies/response \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

Captured bytes are bounded by the 1 MiB storage safety cap and 10 MiB serving
cap. Capture truncation is independent from the 10 MiB request and 100 MiB
non-stream response transport limits.

## Operational endpoints

`GET /ready`, `GET /metrics`, and `GET /health` are outside `/admin/v1` and do
not use admin authentication. See [deployment](deployment.md).
