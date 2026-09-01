# Transport

## Generic Passthrough

All `/v1/*` methods, paths, queries, statuses, end-to-end headers and bodies are
valid for transparent proxying, including unknown endpoints. Preserve raw bytes
and upstream errors. Do not deserialize and reserialize JSON unless a required
transformation cannot be implemented otherwise.

Build the target only from the configured 9router base URL and the client path;
the client cannot select another host. Path joining must not allow `//host` or
similar open-proxy/SSRF behavior.

Retain any configured base path and add or remove only the slash at its boundary
with the request path. Preserve the request query and escaped-path representation
without cleaning duplicate slashes, dot-like segments, or encoded slashes. The
configured scheme and authority always remain authoritative.

## Request Body

Generic endpoints should stream request bodies. Known endpoints may later use a
bounded buffer when metadata inspection or preflight token estimation requires
it, but the exact original bytes must still reach upstream. Separate absolute
body-size and inspect/log-size limits.

While the body bytes are forwarded unchanged, preserve the incoming distinction
between a known `Content-Length`, a known empty body, and an unknown streaming
length. Never buffer a generic body just to calculate its size.

## Headers

Remove hop-by-hop headers in both directions. Replace client Authorization with
the configured upstream credential, and never expose that credential in
responses or logs. Do not preserve `Content-Length` after a body transformation.
Any body transformation must instead recompute the length or remove it.

## Upstream Client

Use one long-lived `http.Client` and `http.Transport` with connection pooling and
keep-alive. Do not set a small `MaxConnsPerHost` that serializes requests. Avoid
a short total client timeout for streaming; use dial, TLS handshake, response
header, idle connection, and request-context deadlines independently.

Disable Go's automatic compression negotiation and decompression on the upstream
transport. Forward a client's `Accept-Encoding` normally and preserve the exact
upstream representation together with its `Content-Encoding` header.

## Response Classification

Choose behavior after receiving actual upstream headers:

- `text/event-stream`: SSE.
- `application/json` or `application/*+json`: JSON.
- anything else: opaque passthrough.

The incoming `stream` value does not determine upstream response format.
Limited byte sniffing may be a fallback for missing/broken Content-Type, not the
primary classifier.

## Cancellation

Bind the upstream request to the incoming `request.Context()`. Client disconnect
must promptly cancel upstream work, release concurrency, reconcile or release
reservations, and record an aborted request. Never continue a billable
generation after downstream cancellation.

Retries are optional before any meaningful downstream bytes. After streaming
starts they are forbidden because replay can duplicate content and tool calls.
