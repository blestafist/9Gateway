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

## `GET /admin/v1/overview`

Returns aggregated request overview statistics, comparisons for an adjacent
previous range of equal duration, active request gauges, key counts, and up to 10
recent request summaries.

```sh
curl -fsS 'http://localhost:8080/admin/v1/overview?after=2026-09-17T00:00:00Z&before=2026-09-18T00:00:00Z' \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

Query parameters:
- `after`: RFC3339 start timestamp (defaults to 24 hours before `before`).
- `before`: RFC3339 end timestamp (defaults to current server time).

Constraints and validation:
- `after` must be strictly before `before`.
- Range duration must not exceed 366 days (1 year).
- Unknown or duplicate parameters return HTTP 400 (`invalid_request`).
- Invalid timestamps return HTTP 400 (`invalid_request`).

Concurrency and caching:
- Bounded to 2 concurrent aggregation queries across the gateway. When capacity
  is saturated, returns HTTP 503 (`service_unavailable`) with `Retry-After: 1`.
- Identical in-flight queries share a single execution (singleflight).
- Completed query results are cached up to 32 entries with a 15-second TTL.
- Cancelled client requests abort query processing without caching partial results.

The response payload contains:
- `current_range_start`, `current_range_end`, `previous_range_start`, `previous_range_end`: RFC3339 timestamps defining current and exact adjacent non-overlapping previous comparison periods.
- `data_timestamp`: UTC timestamp when data was gathered.
- `current` and `previous`: aggregate objects with `total_requests`, `successful_requests`, `error_requests`, `rejected_requests`, and nullable `input_tokens`, `cached_input_tokens`, `output_tokens`, and `cost_micros`.
- `active_requests`: instantaneous count of active in-flight proxy requests.
- `key_counts`: `total` and `enabled` key counts.
- `recent_requests`: list of up to 10 most recent requests (newest first) matching the safe metadata structure returned by `/admin/v1/requests`.

## `GET /admin/v1/usage/timeseries`

Returns aggregated usage metrics grouped into ordered UTC time buckets for
trend analysis and chart rendering.

```sh
curl -fsS 'http://localhost:8080/admin/v1/usage/timeseries?bucket=hour&after=2026-09-17T00:00:00Z&before=2026-09-18T00:00:00Z' \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

Query parameters:
- `after`: optional RFC3339 start timestamp. Omitting `after` requests all
  retained history currently stored in the database (not lifetime data that
  retention has already pruned).
- `before`: optional RFC3339 end timestamp (defaults to current server time).
- `bucket`: optional bucket duration identifier: `five_minutes`, `hour`, `day`,
  `week`, `month`, or `auto` (default `auto` when omitted).

Constraints, bounds, and validation:
- `after` must be strictly before `before` when specified.
- Enforced safe bucket/range combinations:
  - `five_minutes`: range up to 24 hours.
  - `hour`: range up to 31 days.
  - `day`: range up to 2 years (732 days).
  - `week`: range up to 10 years (3660 days).
  - `month`: allowed for longer ranges and all-retained history queries.
- When `bucket` is `auto` or omitted, the gateway selects the finest valid
  interval producing at most 1,000 points.
- Explicitly over-detailed requests (e.g. `five_minutes` over 7 days) and ranges
  producing more than 1,000 buckets are rejected with HTTP 400 (`invalid_request`).
- Unknown or duplicate parameters return HTTP 400 (`invalid_request`).

Concurrency and caching:
- Shares the global 2-query analytics concurrency limit across overview,
  timeseries, and breakdown queries. Saturated capacity returns HTTP 503
  (`service_unavailable`) with `Retry-After: 1`.
- Identical in-flight queries share execution (singleflight).
- Completed query results are cached up to 32 entries with a 15-second TTL.
- Cancelled client requests abort query processing without caching partial results.

The response payload contains:
- `requested_after`: RFC3339 timestamp as requested, or `null` if omitted.
- `effective_after`: RFC3339 timestamp reflecting the actual start boundary used
  (e.g. earliest retained request timestamp when `after` is omitted).
- `before`: RFC3339 end timestamp.
- `bucket`: resolved bucket identifier (`five_minutes`, `hour`, `day`, `week`, or `month`).
- `retention_limited`: boolean indicating whether the requested history window
  was truncated by history retention passes (`true` if retention deleted data
  before `effective_after`, or if `after` was omitted and retention has pruned records).
- `earliest_retained_at`: RFC3339 timestamp of the earliest retained completed request, or `null` if history is empty.
- `latest_retained_at`: RFC3339 timestamp of the latest retained completed request, or `null` if history is empty.
- `buckets`: ordered array of UTC bucket objects (missing buckets within the range are filled explicitly, capped at 1,000 buckets). Each bucket contains:
  - `bucket_start`, `bucket_end`: RFC3339 UTC timestamps.
  - `total_requests`: total finished requests in the bucket.
  - `successful_requests`: requests with terminal outcome `complete` or `custom_dispatch`.
  - `error_requests`: requests with error terminal outcomes (excluding `pre_upstream`), plus requests whose terminal outcome is `NULL`/unknown.
  - `rejected_requests`: requests rejected before upstream dispatch (`pre_upstream`).
  - `input_tokens`, `cached_input_tokens`, `output_tokens`: nullable integer token totals. They are `null` when any request in the bucket has an unknown value; they are `0` for an explicitly known zero, including a filled bucket with no requests.
  - `cost_micros`: nullable integer estimated cost in microdollars. Returns `null` when any request in the bucket has unknown or unestimated cost; returns `0` when all values are known zero or when no requests occurred in the bucket.
  - `avg_total_latency_micros`: nullable integer average total request latency in microseconds (`null` when no latency samples exist in the bucket).
  - `total_latency_samples`: sample count for total latency (allows UI to render gaps rather than misleading zeroes).
  - `avg_ttfb_latency_micros`: nullable integer average time-to-first-byte latency in microseconds (`null` when no samples exist).
  - `ttfb_latency_samples`: sample count for TTFB latency.
  - `avg_upstream_latency_micros`: nullable integer average time-to-upstream-headers latency in microseconds (`null` when no samples exist).
  - `upstream_latency_samples`: sample count for upstream latency.

## `GET /admin/v1/usage/breakdown`

Returns aggregated usage metrics grouped by dimension (`model`, `key`, or `outcome`)
with top-20 ranking and an `other` aggregate.

```sh
curl -fsS 'http://localhost:8080/admin/v1/usage/breakdown?group_by=model&after=2026-09-17T00:00:00Z&before=2026-09-18T00:00:00Z' \
  -H "Authorization: Bearer ${ADMIN_CREDENTIAL}"
```

Query parameters:
- `after`: optional RFC3339 start timestamp. Omitting `after` requests all retained history.
- `before`: optional RFC3339 end timestamp (defaults to current server time).
- `group_by`: required dimension; must be `model`, `key`, or `outcome`.

Constraints, bounds, and validation:
- `group_by` is required and must be strictly `model`, `key`, or `outcome`.
- `after` must be strictly before `before` when specified.
- Unknown or duplicate parameters return HTTP 400 (`invalid_request`).

Concurrency and caching:
- Shares the global 2-query analytics concurrency limit (HTTP 503 with `Retry-After: 1` when saturated).
- Singleflight query deduplication and LRU caching (32 entries, 15-second TTL).
- Cancelled client requests abort processing without caching.

The response payload contains:
- `requested_after`, `effective_after`, `before`: RFC3339 timestamps (`requested_after` is `null` when omitted).
- `group_by`: dimension name (`model`, `key`, or `outcome`).
- `retention_limited`: boolean indicating whether the history window was truncated by retention.
- `earliest_retained_at`, `latest_retained_at`: RFC3339 timestamps of earliest/latest retained records, or `null`.
- `rows`: array of up to 20 ranked dimension rows ordered by `total_requests` descending, tie-broken deterministically:
  - `id`: stable identifier (`model` name, `api_key_id` or `deleted:<key_name>`, `terminal_outcome`, or `"unknown"`).
  - `name`: display name for the entity.
  - `key_id`: optional string containing the key ID when grouping by `key`.
  - `is_unknown`: boolean flag indicating whether the entity was unidentified (`true` for missing model or key).
  - `is_deleted`: boolean flag indicating whether the API key was deleted from key configuration (`true` when grouping by `key` and key is deleted).
  - `total_requests`, `successful_requests`, `error_requests`, `rejected_requests`: integer request counts. `error_requests` includes `NULL`/unknown terminal outcomes; `pre_upstream` remains counted only as rejected.
  - `input_tokens`, `cached_input_tokens`, `output_tokens`: nullable integer token totals. A dimension row is `null` when any request in that row has an unknown value; known zero remains `0`.
  - `cost_micros`: nullable integer estimated cost in microdollars (`null` when any request in that row has unknown or unestimated cost; known zero remains `0`).
  - `other`: aggregate row representing the sum of all dimensions beyond the top 20 (`id: "other"`, `name: "Other"`). Sum of top rows plus `other` matches `total` across all known metrics; if a metric's untruncated total is unknown, the corresponding `other` value remains `null` unless there are no omitted requests.

- `total`: untruncated overall aggregate object containing total counts, nullable token totals, and nullable `cost_micros` across the entire requested range. A token or cost field is `null` when any request in the range has an unknown value, and is `0` for an empty range or known zero.

## Operational endpoints

`GET /ready`, `GET /metrics`, and `GET /health` are outside `/admin/v1` and do
not use admin authentication. See [deployment](deployment.md).
