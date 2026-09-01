# Transport

## Generic Passthrough

All `/v1/*` methods, paths, queries, statuses, end-to-end headers and bodies are
valid for transparent proxying, including unknown endpoints. Preserve raw bytes
and upstream errors. Do not deserialize and reserialize JSON unless a required
transformation cannot be implemented otherwise.

Build the target only from the configured 9router base URL and the client path;
the client cannot select another host. Path joining must not allow `//host` or
similar open-proxy/SSRF behavior.

## Request Body

Generic endpoints should stream request bodies. Known endpoints may later use a
bounded buffer when metadata inspection or preflight token estimation requires
it, but the exact original bytes must still reach upstream. Separate absolute
body-size and inspect/log-size limits.

## Headers

Remove hop-by-hop headers in both directions. Replace client Authorization with
the configured upstream credential, and never expose that credential in
responses or logs. Do not preserve `Content-Length` after a body transformation.

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
