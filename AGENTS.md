# Agent Rules

## Purpose

Build a lightweight OpenAI-compatible policy proxy in front of 9router:

`Client -> Gateway -> 9router -> Provider`

The gateway owns authentication, limits, accounting, request inspection and
transparent HTTP/SSE proxying. 9router owns provider routing and protocol
translation. Default to transparent behavior and intervene only for policy,
accounting, or explicit streaming compatibility.

## Required Context

For every task, read only:

1. `AGENTS.md`.
2. `CURRENT.md`.
3. The named task in `TASKS.md`.
4. Architecture documents explicitly linked by that task, if needed.

`PLAN.md` is product reference. Read it only for disputed product decisions or
scope changes. `docs/archive/ARCHITECTURE-full.md` and
`docs/tasks/backlog-full.md` are historical/cold references and are not routine
task context.

## Core Responsibilities

- Authenticate gateway API keys.
- Enforce request, token, budget and concurrency limits.
- Reserve before concurrent work and reconcile against actual usage.
- Account for usage and cost without blocking transport.
- Inspect requests and responses under bounded, secret-safe logging policies.
- Transparently proxy generic `/v1/*` methods, paths, queries, headers and bodies.

## Do Not

- Implement provider routing or load balancing.
- Translate Anthropic, OpenAI, Gemini or provider-specific protocols.
- Modify prompts, tool calls, reasoning content or unknown payload fields.
- Buffer SSE unless conversion to a non-stream response explicitly requires it.
- Wait for `[DONE]` when upstream has already ended with EOF.
- Serialize requests unless a configured limit requires it.
- Retry after meaningful response bytes have reached the client.
- Add Redis, PostgreSQL, a plugin framework or Web UI before the active milestone.
- Implement future tasks while completing the current task.

## Transport Invariants

- Determine response format from actual upstream `Content-Type`, not request
  assumptions.
- For transparent SSE: read upstream, write downstream, flush, repeat.
- Upstream EOF closes downstream immediately; `finish_reason` does not control
  transport lifetime.
- Client cancellation cancels the upstream request and releases all resources.
- `stream:false` plus upstream SSE is aggregated into OpenAI-compatible JSON.
- Generic `/v1/*` endpoints pass through even when the gateway does not know
  their schema.
- Preserve raw bytes and upstream status whenever transformation is unnecessary.
- Limiting, parsing, telemetry and SQLite must not delay first-byte, per-chunk,
  or stream-close delivery.

## Task Discipline

- One task has one goal. Keep changes within its acceptance criteria.
- Prefer the smallest correct implementation; do not create speculative
  interfaces or empty package boilerplate.
- Add behavior tests in the same task as HTTP or streaming behavior.
- Do not leave broken tests or defer acceptance criteria to the next task.
- If a task truly requires a broad unrelated refactor, split the task before
  implementing it.
- Update `CURRENT.md` after completing a task: move its ID to `Done`, select the
  next task, and retain only immediate milestone state.

## Orchestration

- Orchestrators must read each named task themselves and give the worker a
  self-contained assignment; never tell the worker to discover or read the task.
- Use one worker per task. Execute dependent tasks sequentially, not as one
  batched worker assignment.
- Each task worker must implement, verify, update `CURRENT.md`, and commit.
- After the requested series, launch review agent(s); have worker(s) fix
  confirmed findings, re-verify, and commit fixes, repeating review as warranted.

## Verification

After every implementation task run, when the Go module exists:

```text
go fmt ./...
go test ./...
go build ./...
```

If a command is not applicable yet, state that explicitly in the task result.



### Commit

Do commit in style as commit history (git log) presents. Do it after completing a task Txxx.

---

Update planning files / docs by yourself if you want sth to remember. Do not turn it into a huge docs. Current focus only
