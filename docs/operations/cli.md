# `gwctl` CLI reference

`gwctl` is a stateless HTTP client for the admin API. It never stores
credentials on disk and never opens SQLite. Build it with Go 1.23 or newer:

```sh
go build -o gwctl ./cmd/gwctl
```

## Global options

```text
gwctl [global options] <command>
```

- `--gateway-url URL`: absolute HTTP/HTTPS gateway base URL, default
  `http://localhost:8080`; no query, fragment, or userinfo;
- `--admin-credential VALUE`: admin credential, or `GWCTL_ADMIN_CREDENTIAL`;
- `--help`/`-h`: show usage and exit 0.

Global options and help are accepted before or after the command. The
credential is required for gateway calls, but not `version`.

## Commands

### `version`

```sh
gwctl version
```

Prints version, short commit, build date, Go version, and OS/arch without
validating URL or contacting the gateway.

### `ping`

```sh
gwctl [global options] ping
```

Calls `GET /admin/v1/keys?limit=1`, validates successful JSON, and prints
`Gateway reachable and admin authentication succeeded.`

### `keys create`

```sh
gwctl keys create NAME [--json]
```

Posts `{"name":"NAME"}`. Human output prints ID, name, raw key, and the
one-time-save warning. `--json` prints the raw creation response, including
the one-time key. Exactly one non-empty name is required.

### `keys list`

```sh
gwctl keys list [--limit N] [--json]
```

Fetches all pages using 50-item pages. `--limit N` stops at N records (positive,
maximum 100000); without it, all pages are fetched. Multi-page progress goes
to stderr. Human output columns are `ID` (first 12), `Name`, `Prefix`,
`Enabled`, and local-time `Created`. `--json` prints an aggregated `keys`
object and keeps API RFC3339 values. Empty human output is `No keys found`.

### `keys get`

```sh
gwctl keys get KEY_ID [--json]
```

Fetches one key detail. IDs are ASCII letters, digits, `-`, `_`, maximum 256
bytes, with no leading `-` or `_`. Human output has metadata and policy
sections, local timestamps, and `-` for null values. `--json` prints API JSON;
the raw key is never returned by this endpoint.

### `requests list`

```sh
gwctl requests list [--limit N] [--key-id KEY_ID] \
  [--after RFC3339] [--before RFC3339] [--json]
```

Fetches history in 50-item pages. `--limit` is positive and at most 100000;
`--key-id` uses the key ID grammar; times must be RFC3339 and `after` cannot
be later than `before`. Progress goes to stderr. Human columns are `Request
ID` (first 12), `Key Name`, `Method`, `Route`, `Model`, `Status`, `Tokens`,
`Cost`, `Duration`, `Completed`; timestamps are local, tokens comma-separated,
durations human-readable, and cost is dollars. `--json` prints an aggregated
`requests` object. Empty human output is `No requests found`.

### `requests get`

```sh
gwctl requests get REQUEST_ID [--json]
gwctl requests get REQUEST_ID --body KIND [--output FILE]
```

Without `--body`, prints identity, request, response, usage/cost, timing, and
error sections. The CLI accepts exactly 32 lowercase hexadecimal request-ID
characters. With `--body`, `KIND` is `client_request`, `upstream_request`, or
`response`; bytes are exact to stdout or atomically to `FILE`. `--json` cannot
be combined with `--body`; `--output` requires it. Truncation warnings go to
stderr. There is no additional body output mode.

## Output and exit behavior

Data is stdout; progress, warnings, and diagnostics are stderr.

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Usage/local validation error (missing credential, invalid URL, option, or ID); usage text is printed. |
| `2` | API, authentication, connection, malformed-response, or output error; diagnostics are sanitized. |

The binary passes the returned code to `os.Exit`; library callers of
`internal/gwctl.Run` receive it without process termination. Typical sanitized
messages are `Authentication failed`, `Connection failed`, `Invalid API
response`, `Key not found`, and `Request not found`.
