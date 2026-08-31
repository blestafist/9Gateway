# Streaming

## Transparent SSE

The hot path is:

`read upstream -> write downstream -> flush -> repeat -> EOF -> close`

Flush every successful streaming write. No SQLite write, tokenizer, pricing
lookup, blocking telemetry operation, or full event reconstruction may precede
the flush. A lightweight observer may inspect chunks but cannot govern normal
transport lifetime. If observation becomes asynchronous, copy or transfer
buffer ownership before reusing the read buffer.

## Termination

Upstream EOF immediately ends downstream, including streams without `[DONE]`.
`finish_reason` is metadata, not a close signal. `[DONE]` passes through unchanged
but is not required when upstream ends normally. Do not add heartbeat or use an
idle/grace timeout to delay an EOF that already occurred.

A compatibility grace timeout for a genuinely hanging upstream is a separate,
explicit, disabled-by-default policy. Never reproduce a multi-second delay after
the upstream body has physically ended.

## SSE Framing

The generic parser supports `event:`, one or more `data:` lines, comments, blank
line event termination, split reads, and multiple events per read. It enforces a
bounded event size and knows nothing about OpenAI. Read boundaries are never
assumed to be line or event boundaries.

OpenAI observation extracts ID, model, choices, role, content, finish reason,
tool calls, usage, and `[DONE]`. Parse failures in passive observation are
telemetry errors and do not break otherwise healthy passthrough.

## SSE To JSON

When a client explicitly requests `stream:false` and upstream actually returns
SSE, buffer and aggregate the stream into one OpenAI-compatible JSON response.
Support multiple choices, role/content deltas, finish reason, usage, and indexed
tool calls. Concatenate fragmented function arguments before any JSON decoding.

Aggregation completes on `[DONE]` or valid EOF without `[DONE]`. EOF before any
meaningful response is a protocol/upstream error. Usage can arrive after a
terminal content chunk, so `finish_reason` alone does not complete aggregation.
