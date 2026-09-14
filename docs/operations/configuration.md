# Configuration reference

The gateway reads one YAML document from the path passed to
`gateway --config PATH`. YAML decoding uses strict known-field handling. The
loader applies defaults, resolves environment references, validates all fields,
and only then opens SQLite and starts the listener.

## Top-level fields

| YAML field | Type | Default | Valid values / behavior |
| --- | --- | --- | --- |
| `listen_addr` | string | none | Required `host:port` address. Port is 1-65535. A non-root process cannot bind a port below 1024. `:8080` is the Compose default. |
| `upstream_base_url` | string | none | Required absolute `http` or `https` URL with a host. Fragments and embedded userinfo are rejected. It must not point at the gateway's own listen address. The path, if present, is retained as the upstream base path. |
| `upstream_api_key` | string | none | Required and non-empty after resolution. A literal is accepted, or an exact `${ENV_NAME}` reference is resolved from the process environment. |
| `sqlite_path` | string | `/data/gateway.db` | Required after defaults. Use `:memory:` only for tests. A file must be regular and writable; a new file requires an existing writable and searchable parent directory. |
| `auth_pepper` | string | none | Required exact `${ENV_NAME}` reference; literal values and malformed references are rejected. The referenced variable must exist and be non-empty. |
| `admin_credential` | string | none | Required exact `${ENV_NAME}` reference; literal values and malformed references are rejected. The referenced variable must exist and be non-empty, and must differ from `upstream_api_key`. |
| `tokenizer` | mapping | see below | Optional. An omitted, empty, or null mapping receives tokenizer defaults. |
| `observability` | mapping | see below | Optional. An omitted or empty mapping receives observability defaults. An explicit `null` is rejected. |
| `pricing` | mapping | no rules | Optional. It may contain only `rules`; omission, `{}`, or `rules: []` means no configured prices. |
| `shutdown_timeout_seconds` | integer | `30` | `0` means the default is applied when loading YAML. After defaults it must be positive and no greater than `600` seconds. |

Environment references are not shell interpolation: the entire scalar must be
`${NAME}`, where `NAME` starts with an ASCII letter or `_` and continues with
ASCII letters, digits, or `_`. Values containing `${...}` as a substring are
invalid. Secret values are never included in validation errors.

## `tokenizer`

| YAML field | Type | Default | Range / behavior |
| --- | --- | --- | --- |
| `mode` | string | `estimate` | `estimate` or `usage_only`. |
| `max_inspected_request_bytes` | integer | `65536` (64 KiB) | Positive, at most `16777216` (16 MiB). An explicitly supplied zero is invalid. |
| `fallback_unknown_input_tokens` | integer | `4096` | Positive, at most `1000000000`. An explicitly supplied zero is invalid. |
| `fallback_max_output_tokens` | integer | `4096` | Positive, at most `1000000000`. An explicitly supplied zero is invalid. The two maxima must have a safe sum within `int64`. |

`usage_only` uses usage reported by the upstream for token accounting; the
gateway's estimator is the default `estimate` mode. These settings are
deployment-wide and do not define per-key policy.

## `observability`

All four values are strict YAML integers, not duration strings. `0` is allowed
only for `max_captured_body_bytes` and disables body capture globally.

| YAML field | Type | Default | Range / behavior |
| --- | --- | --- | --- |
| `telemetry_queue_capacity` | integer | `128` | Positive, at most `4096`. |
| `max_captured_body_bytes` | integer | `0` | `0` through `1048576` (1 MiB). This is a capture bound, not the request/response enforcement limit. |
| `request_retention_seconds` | integer | `2592000` (30 days) | Positive, at most `31536000` (365 days). |
| `body_retention_seconds` | integer | `604800` (7 days) | Positive, at most `31536000` (365 days), and no greater than `request_retention_seconds`. |

Retention cleanup runs at worker startup and every 1024 processed jobs. Each
pass is capped at 1000 body rows and then 1000 metadata rows; those caps are
implementation constants, not YAML fields.

## `pricing`

`pricing` contains one optional field:

```yaml
pricing:
  rules:
    - model: mock-model
      input_per_million_micros: 1000000
      output_per_million_micros: 2000000
```

Each rule requires a non-empty valid UTF-8 exact or slash-aware glob `model`,
and non-negative integer `input_per_million_micros` and
`output_per_million_micros` values no greater than
`9223372036854775807`. Unknown/duplicate fields, duplicate selectors,
malformed globs, and wrong scalar types are rejected. Exact selectors win over
globs; globs are considered in declaration order. Prices are integer USD
micros per million tokens.

## Per-key policy (admin API JSON)

Per-key policies are JSON, not YAML. The empty object `{}` is unrestricted.
Unknown fields and duplicate JSON keys are rejected. Supported fields are:

| JSON field | Type / default | Behavior |
| --- | --- | --- |
| `allowed_models` | string array, empty | If non-empty, at least one pattern must match. |
| `denied_models` | string array, empty | A matching deny pattern takes precedence over allow. |
| `request_windows` | array, empty | Entries are `{ "amount": positive integer, "duration": positive whole-second Go duration string }`. |
| `token_windows` | array, empty | Entries are `{ "amount": positive int64, "duration": positive whole-second Go duration string }`. |
| `token_mode` | deployment tokenizer mode | `usage_only` or `estimate`; an explicit value overrides the deployment default. |
| `max_concurrent_requests` | `0` | Non-negative integer; zero means unlimited. |
| `budget_limits` | array, empty | Entries are `{ "period": "total"|"day"|"month", "amount_micros": positive int64 }`; each period may occur once. |
| `log_request_body` | `false` | Per-key opt-in, still bounded by deployment `max_captured_body_bytes`. |
| `log_response_body` | `false` | Per-key opt-in for bytes delivered downstream, with the same deployment bound. |

See [admin API](admin-api.md) for policy update envelopes.
