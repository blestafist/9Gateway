# Streaming

## Transparent SSE

The hot path is:

`read upstream -> write downstream -> flush -> repeat -> EOF -> close`

Flush every successful streaming write. No SQLite write, tokenizer, pricing
lookup, blocking telemetry operation, or full event reconstruction may precede
the flush. The transparent implementation reads into a bounded buffer, writes
the bytes as received, flushes through `http.ResponseController`, and repeats
without interpreting read boundaries as event boundaries. A lightweight
observer may inspect chunks but cannot govern normal transport lifetime. If
observation becomes asynchronous, copy or transfer buffer ownership before
reusing the read buffer.

## Termination

Upstream EOF immediately ends downstream, including streams without `[DONE]`.
`finish_reason` is metadata, not a close signal. `[DONE]` passes through unchanged
but is only payload for transparent transport and is not required when upstream
ends normally. Comments pass through unchanged; the gateway does not generate
heartbeats. Do not add an idle/grace timeout to delay an EOF that already occurred.

A compatibility grace timeout for a genuinely hanging upstream is a separate,
explicit, disabled-by-default policy. Never reproduce a multi-second delay after
the upstream body has physically ended.

## SSE Framing

The protocol-neutral parser exposes `SSEEvent{Event, Data}` through a sequential
reader constructed with an explicit positive maximum event size in bytes. It
returns no fabricated event for empty input and returns `io.EOF` after the final
complete event. Returned event strings are owned by the caller. The parser is
separate from the transparent transport hot path.

The generic parser supports `event:`, one or more `data:` lines, comments, blank
line event termination, split reads, and multiple events per read. Data lines
within one event are joined with a newline; an optional single space after the
field colon is removed, and unknown fields are ignored. An event is emitted
only when at least one non-comment field was seen, so empty input or comments
alone do not fabricate an event. The T035 contract already enforces a bounded
frame size; field-level semantics are implemented in T036-T040. The parser
knows nothing about OpenAI, and read boundaries are never assumed to be line or
event boundaries.

Future OpenAI observation extracts ID, model, choices, role, content, finish reason,
tool calls, usage, and `[DONE]`. Parse failures in passive observation are
telemetry errors and do not break otherwise healthy passthrough.

## SSE To JSON

When a client explicitly requests `stream:false` and upstream actually returns
SSE, a future compatibility layer may buffer and aggregate the stream into one
OpenAI-compatible JSON response.
Support multiple choices, role/content deltas, finish reason, usage, and indexed
tool calls. Concatenate fragmented function arguments before any JSON decoding.

Aggregation completes on `[DONE]` or valid EOF without `[DONE]`. EOF before any
meaningful response is a protocol/upstream error. Usage can arrive after a
terminal content chunk, so `finish_reason` alone does not complete aggregation.
