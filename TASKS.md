# Active Tasks (T161-T180)

This file contains the next twenty atomic tasks for the first complete Web UI
milestone. The result is a responsive operations console for the capabilities
9Gateway actually owns: health, usage, API keys, request history, captured
bodies, and safe runtime information. It must not expose or imitate 9router
provider routing, protocol translation, proxy pools, or provider credentials.

Analytics direction is deliberately "Bifrost-lite": borrow the useful hierarchy
of KPI totals, time-series, model/key rankings, cost, tokens, success rate, and
latency, but keep one primary analysis canvas and one contextual ranking section.
Do not copy Bifrost's provider/model catalog, realtime log stream, exports,
dimension tabs, broad filter matrix, or multiple competing charts above the fold.

An implementation agent reads only `AGENTS.md`, `CURRENT.md`, its assigned task,
and the architecture documents linked by that task. Tasks are ordered by
dependency and must not be implemented out of order. UI screenshots placed in
`docs/ui/reference/` are visual references, not product requirements: preserve
9Gateway's information architecture and do not copy unsupported navigation.

The fixed frontend baseline for this milestone is React, TypeScript, and Vite in
`web/`, built with npm and committed `package-lock.json`. Use React Router for
deep links, TanStack Query for server state, Lucide SVG icons, Recharts for
charts, Vitest/Testing Library for component tests, and Playwright for browser
tests. Prefer native HTML and project-owned CSS variables/components over a
large component framework. The production assets are embedded into the Go
binary and served same-origin under `/ui/`; no Node.js runtime is shipped.

Every implementation task must finish with the applicable frontend checks
(`npm --prefix web run lint`, `npm --prefix web run test`, and
`npm --prefix web run build`) plus `go fmt ./...`, `go test ./...`, and
`go build ./...`. Add behavioral tests in the same task as changed HTTP or UI
behavior. Update `CURRENT.md` and commit after completing one task.

Resource efficiency is a release requirement, not a final polish item:

- Initial production JavaScript must stay below 150 KiB gzip; each lazy route
  chunk below 180 KiB gzip; all JavaScript below 500 KiB gzip; application CSS
  below 50 KiB gzip. T179 enforces these budgets and any increase requires a
  measured, documented reason in the task that introduces it.
- Lazy-load charts, editors, body viewers, and non-current routes. Do not ship
  remote fonts, large icon packs, videos, WebGL/canvas backgrounds, GSAP, a
  service worker, or decorative libraries. Import individual Lucide icons.
- Avoid large-area `backdrop-filter`, continuous animation, layout animation,
  and JS-driven visual effects. Animate only `transform`/`opacity`, stop work in
  hidden tabs, and render the final state immediately under reduced motion.
- Use cursor pages of at most 100 rows in the UI, at most 1,000 plotted points,
  and at most 20 breakdown rows plus `other`. Do not retain prior pages or body
  bytes after their view is closed. Keep routine rendered DOM below 1,500 nodes.
- Overview polling is no faster than 30 seconds and System polling no faster
  than 60 seconds; Usage, Keys, Requests, and details refresh only on navigation,
  filter/page changes, mutation invalidation, or explicit user action. Never poll
  bodies. All polling stops while hidden, offline, logged out, or unmounted.
- The browser permits at most four concurrent admin reads and one mutation.
  Superseded work is cancelled. Server analytics permit at most two concurrent
  aggregate queries, use a small bounded TTL cache/singleflight for identical
  ranges, and fail fast with a safe retryable response when capacity is full.
- UI/API work must not measurably regress proxy first-byte, SSE per-chunk, or
  stream-close behavior. T166/T168 add representative SQLite query budgets;
  T179 measures CPU, memory, startup, bundles, DOM, and proxy-path impact.

Frontend modularity is also a milestone invariant. Use a feature-first layout:
`web/src/app` for composition/routing/providers, `web/src/features/<feature>` for
route-owned API types, queries, components, and tests, and `web/src/shared` only
for proven cross-feature primitives. Each feature exposes a small public entry
point; features do not import another feature's internals. Keep route files as
composition boundaries, colocate tests/fixtures with their owner, and avoid
global stores, generic `utils`, barrel files that re-export the whole tree, and
single files that accumulate unrelated page/API/chart logic. Split by behavior
when a module becomes difficult to understand in isolation, not by arbitrary
one-component-per-file ceremony. Backend UI/session/analytics handlers follow
the same narrow-package rule and must not enlarge the proxy orchestration files.

## Foundation and Access

### T161 - Scaffold the embedded Web UI build

Goal: create the smallest production-capable React/TypeScript application and
serve its compiled assets from the gateway without changing proxy behavior.

Scope:

- Add `web/` with Vite, React, strict TypeScript, ESLint, Vitest, Testing Library,
  npm scripts, deterministic `package-lock.json`, and a minimal smoke page.
- Establish the feature-first directories and import-boundary lint rules from the
  milestone invariant. Add a short `web/README.md` map explaining where a task
  should place route, feature, shared, fixture, and test code so later agents can
  load only the feature they are changing.
- Build to a directory embedded by Go. Serve `/ui/` assets with correct MIME
  types, immutable caching for hashed assets, no-cache for `index.html`, and SPA
  fallback only below `/ui/`; redirect `/ui` to `/ui/`.
- Support development through Vite with a same-origin-style proxy to a local
  gateway. Keep production asset serving independent of Node.js.
- Add one Go route test and one frontend smoke test. Unknown `/v1/*`, admin,
  health, readiness, and metrics routes must retain their existing behavior.
- Keep the initial page intentionally plain; design and navigation belong to
  later tasks.

Acceptance and tests:

- A production frontend build is served by the compiled gateway at `/ui/`, and
  refreshing a nested `/ui/example` route returns the SPA document.
- Missing hashed assets return 404 rather than `index.html`. `HEAD` works, and
  unsupported methods do not mutate or proxy UI files.
- `go build ./cmd/gateway` succeeds after a clean frontend build; documented
  development commands start Vite and the gateway together.
- The gateway remains buildable from a release source tree using the committed
  production assets/build contract; Docker integration is deferred to T180.
- A boundary test/lint fixture proves features cannot reach into another
  feature's internals or import application composition from shared code.

Reference: `docs/architecture/repository.md`,
`docs/architecture/transport.md`.

Dependencies and out of scope: first task in T161-T180. Do not add navigation,
authentication, charts, Tailwind, a component framework, or admin features.

### T162 - Establish the visual system and component primitives

Goal: define a distinctive, accessible operations-console visual language that
can be applied consistently to every later screen.

Scope:

- Add `docs/ui/design-system.md` as the source of truth for color, typography,
  spacing, radii, elevation, density, iconography, motion, breakpoints, and data
  visualization. Record which decisions came from `docs/ui/reference/`.
- Use a dark technical dashboard as the primary direction: charcoal/slate
  surfaces, restrained warm-coral brand accent, semantic green/amber/red/blue,
  a CSS-only subtle grid texture, compact data density, and limited translucency.
  Do not use large blurred layers or filters on scrolling surfaces. Provide
  a fully usable light theme, not a color inversion.
- Implement semantic CSS tokens and reusable primitives needed by this
  milestone: Button, IconButton, Input, Select, Checkbox/Switch, Badge, Card,
  Tabs, Tooltip, Dialog/Drawer, Table shell, Skeleton, EmptyState, Alert, and
  Toast region. Use Lucide icons, never emoji as UI icons.
- Keep primitive APIs narrow and composable. Product-specific cards, filters,
  tables, and chart wrappers stay in their owning feature until a second real
  consumer proves a shared abstraction is useful.
- Add a development-only `/ui/components` catalog showing states, focus,
  disabled controls, long text, errors, skeletons, and both themes. Do not ship
  this route in production.
- Use system/local fonts so the UI has no runtime dependency on Google Fonts or
  another third-party asset host.

Acceptance and tests:

- Text and controls meet WCAG AA contrast; focus is always visible; interactive
  targets are at least 44x44 CSS pixels where space permits and never rely on
  hover alone.
- Components work by keyboard and expose accessible names/states. Dialog focus
  is trapped and restored. Toasts use the appropriate live-region semantics.
- Themes render without a flash of the wrong theme and respect reduced motion,
  contrast preferences, browser zoom, and 200% text scaling.
- Component tests cover keyboard interaction and representative variants; no
  raw product colors are introduced inside feature components later.

Reference: UI screenshots in `docs/ui/reference/` when present.

Dependencies and out of scope: depends on T161. Do not build product pages,
charts, authentication, marketing UI, or a public component package.

### T163 - Build the responsive application shell

Goal: provide the stable navigation, routing, page framing, and responsive
behavior shared by the complete console.

Scope:

- Add routes for Overview, Usage, API Keys, Requests, and System, plus Login,
  Not Found, and a generic route-error screen. Every product page gets a stable
  deep link below `/ui/`.
- Make each route a lazy feature entry with its own error/loading boundary. The
  shell may depend on shared primitives, but it must not import feature internals
  or centralize page-specific state.
- Implement desktop sidebar, compact sidebar, mobile drawer, page header,
  breadcrumbs where useful, active-route state, skip link, and a connection/
  version area reserved for later data.
- Persist theme and sidebar preference locally. Do not persist credentials,
  response bodies, API records, filters containing secrets, or query cache.
- Use CSS layout that works from 320px to ultrawide displays. Tables may switch
  to cards or controlled internal scrolling, but the document must not gain
  accidental horizontal scrolling.
- Add route-level code splitting and predictable page titles. Navigation must
  remain usable while a page chunk is loading or has failed.

Acceptance and tests:

- Keyboard and screen-reader users can identify and operate navigation, skip to
  main content, close the mobile drawer, and see the current page.
- Direct navigation and refresh work for every route in production embedding.
- Layout tests cover 375, 768, 1024, and 1440 CSS-pixel viewports, long labels,
  browser zoom, and mobile safe-area insets.
- Loading the shell does not download chart/editor/body-viewer code. Route chunk
  and DOM-size measurements stay within the milestone resource budgets.
- Unsupported 9router sections such as Providers, Combo/Vision, Proxy Pools,
  Skills, and routing topology do not appear in 9Gateway navigation.

Reference: `docs/architecture/repository.md`, UI screenshots in
`docs/ui/reference/` when present.

Dependencies and out of scope: depends on T162. Pages contain placeholders only;
do not connect admin data or implement login yet.

### T164 - Add secure browser sessions and the login flow

Goal: let an operator authenticate with the existing admin credential without
storing that credential in JavaScript-accessible storage or sending it after
login.

Scope:

- Add `POST /admin/ui/v1/session`, `GET /admin/ui/v1/session`, and
  `DELETE /admin/ui/v1/session`. Validate the admin credential once, then issue
  a cryptographically random, bounded, in-memory session with idle and absolute
  expiry and an opaque HttpOnly SameSite=Strict cookie.
- Set `Secure` when the request is HTTPS (including trusted configured proxy
  handling), scope the cookie to admin/UI routes, rotate it on login, and clear
  it on logout/expiry. Bound total sessions and revoke all sessions on restart.
- Accept the web session on existing `/admin/v1/*` handlers while retaining
  Bearer authentication for CLI clients. Require a session-bound CSRF token and
  same-origin validation for unsafe cookie-authenticated methods; Bearer clients
  remain unaffected.
- Add the Login screen, protected-route handling, session bootstrap, explicit
  logout, expired-session recovery, password-manager-compatible fields, and
  non-enumerating error messages. Keep the submitted credential only in local
  component state and clear it after the attempt.
- Rate-limit failed login attempts with a small bounded in-memory limiter that
  cannot grow from attacker-controlled cardinality.

Acceptance and tests:

- `localStorage`, `sessionStorage`, URL, history, logs, DOM after login, and
  frontend error telemetry never contain the admin credential or session ID.
- CSRF, cross-origin, duplicated cookie/header, fixation, expiry, logout,
  restart, and brute-force tests pass. Existing admin Bearer API tests stay green.
- Login and expiry are accessible, do not reveal whether a guessed credential
  prefix was correct, and return the user to the originally requested UI route.
- Security headers for UI/session responses include a restrictive CSP,
  `frame-ancestors 'none'`, `nosniff`, and a safe referrer policy.

Reference: `docs/architecture/operations.md`,
`docs/operations/admin-api.md`.

Dependencies and out of scope: depends on T163. Do not add users, roles, OAuth,
password reset, persistent sessions, or remote identity providers.

### T165 - Implement the typed admin data layer

Goal: provide one tested frontend boundary for admin API calls, errors,
pagination, cancellation, formatting, and server-state caching.

Scope:

- Define TypeScript types matching current key, policy, request, body, readiness,
  and safe error envelopes. Add narrow runtime validation at the network
  boundary for data that drives rendering; do not blindly cast JSON.
- Keep a small shared transport/error layer while each feature owns its endpoint
  schema, query keys, mapping, and fixtures. Do not create one monolithic API
  client/types file that every future task must read or edit.
- Build a same-origin fetch client with credentials, CSRF support, request
  cancellation, bounded response reads, typed errors, and one re-auth transition
  on 401. Never retry mutations automatically.
- Configure TanStack Query with conservative stale times and disabled focus
  refetch for detail/body views. Clear all cached admin data on logout or expiry.
- Enforce the browser concurrency budget centrally, abort obsolete reads, and
  avoid retries while offline. Retry at most once for safe transient GET failures
  with bounded jitter; never spin or create a request storm.
- Add shared formatting for RFC3339 timestamps, durations, integer micro-dollar
  costs, token counts, byte counts, nullable values, statuses, and outcomes.
- Add cursor-pagination helpers whose URL filters are shareable while opaque
  cursors and sensitive loaded content remain out of browser history/storage.

Acceptance and tests:

- Contract fixtures from real Go handler responses decode successfully; malformed
  and oversized responses produce a safe UI error rather than partial rendering.
- Abort signals cancel requests on navigation. A late response cannot populate a
  cache after logout or overwrite newer filter results.
- Formatting is locale-aware but preserves exact values in labels/tooltips where
  rounding could mislead. Unknown enum values render safely.
- Unit tests cover 401, 404, 409, 429, 5xx, invalid JSON, offline, cancellation,
  null-vs-zero values, and cursor transitions.

Reference: `docs/operations/admin-api.md`,
`docs/architecture/observability.md`.

Dependencies and out of scope: depends on T164. Do not generate a public SDK,
introduce GraphQL, persist query cache, or change existing API wire formats.

## Overview and Analytics

### T166 - Add the admin overview aggregation API

Goal: expose bounded summary data needed by the overview without making the
browser scrape Prometheus or download the full request history.

Scope:

- Add authenticated `GET /admin/v1/overview?after=RFC3339&before=RFC3339` with a
  default last-24-hours range and presets supported through explicit bounds up
  to one year. Overview does not implement all-time time series; the dedicated
  Usage API owns that potentially heavier query.
- Return request count, success/error/rejection counts, input/cached/output
  tokens, nullable total estimated cost micros, active request gauge, key counts
  (total/enabled), and up to 10 recent request summaries. Include the same
  aggregate values for the immediately preceding equal-length period so the UI
  can show honest deltas without issuing another browser request.
- Compute completed-period values in SQLite with indexed aggregate queries and
  obtain live gauges without blocking transport. Preserve unknown cost/token
  semantics separately from known zero and report the effective range/data time.
- Add only the indexes proven necessary by query-plan tests. Keep endpoint work
  bounded independently of retained history size and admin disconnect-aware.
- Share identical in-flight queries and cache at most 32 completed range results
  for no more than 15 seconds. Limit overview/usage aggregation globally to two
  concurrent queries and return a retryable 503 when that capacity is exhausted.

Acceptance and tests:

- HTTP/repository tests cover empty data, all terminal outcomes, nullable usage,
  exact boundaries, invalid/oversized ranges, retention gaps, and concurrent
  writes while reading.
- Totals match underlying request rows and never double count. Response contains
  no bodies, raw keys, credentials, policy JSON, SQL, or unbounded labels.
- Previous-period boundaries are exact and non-overlapping. Percentage change is
  computed in the UI from exact values; a zero/unknown baseline is represented as
  unavailable rather than infinity or a misleading 100% change.
- Query-plan and representative-volume tests show indexed range scans and a
  p95 under 150ms on the documented fixture hardware/data set, peak temporary
  allocation below 8 MiB per query, and no measurable proxy latency regression.
- Existing request-history and metrics behavior remains unchanged.

Reference: `docs/architecture/storage.md`,
`docs/architecture/accounting.md`, `docs/architecture/observability.md`.

Dependencies and out of scope: depends on T165. Do not add time-series buckets,
provider topology, billing invoices, forecasts, or live WebSockets.

### T167 - Build the operational Overview page

Goal: make the default page answer whether the gateway is healthy, busy, and
successfully serving traffic at a glance.

Scope:

- Connect T166 into a period selector, metric cards, health/status strip, recent
  requests list, key summary, and clear links to deeper Usage, Keys, Requests,
  and System screens.
- Show compact previous-period deltas on request, token, cost, and error cards
  with direction, exact comparison text, and neutral treatment where an increase
  is neither inherently good nor bad. Error/rejection increases use warning
  semantics; cost remains explicitly estimated.
- Use the visual hierarchy of the supplied dashboard reference without its
  provider graph. Emphasize request outcomes, tokens, estimated cost, active
  work, and freshness rather than decorative data.
- Poll only while the page is visible, no faster than every 30 seconds, with a manual
  refresh action. Show last updated, paused/offline/stale states, and preserve the
  previous snapshot during a background refresh.
- Provide honest empty, partial-data, loading, offline, expired-session, and
  backend-error states. Label all cost figures as estimates.

Acceptance and tests:

- The page is useful with no requests, unknown usage/cost, thousands of requests,
  long model/key names, and a temporarily unavailable backend.
- Metrics never animate from zero in a misleading way. Freshness is textual and
  not communicated by color alone.
- Polling stops in hidden tabs and after navigation/logout; manual refresh cannot
  create overlapping requests.
- Component/integration tests cover period changes, stale data, partial nulls,
  links, polling lifecycle, and responsive layouts.

Reference: `docs/architecture/observability.md`, UI screenshots in
`docs/ui/reference/` when present.

Dependencies and out of scope: depends on T166. Do not implement detailed charts,
provider nodes, cost forecasts, drag-and-drop cards, or dashboard customization.

### T168 - Add bounded usage time-series and breakdown APIs

Goal: support useful historical analysis with explicit ranges, bucket sizes, and
dimensions while keeping SQLite work predictable.

Scope:

- Add authenticated `GET /admin/v1/usage/timeseries` accepting optional `after`,
  `before`, and `bucket` (`five_minutes`, `hour`, `day`, `week`, or `month`).
  Omitting `after` requests all retained history, not lifetime data that retention
  has already deleted. Return ordered UTC buckets for total/successful/
  errored/rejected requests, input/cached/output tokens, nullable estimated cost
  micros, and nullable average total/TTFB/upstream latency micros. Each latency
  includes its contributing sample count so absent observations are not shown as
  zero.
- Add authenticated `GET /admin/v1/usage/breakdown` accepting the same optional range and
  `group_by` (`model`, `key`, `outcome`). Return the top 20 rows plus an `other`
  aggregate, with request/token/cost values and stable identifiers where safe.
- Enforce safe bucket/range combinations: five-minute up to 24 hours, hourly up
  to 31 days, daily up to 2 years, weekly up to 10 years, and monthly for longer
  or all-retained ranges. If `bucket=auto` or omitted, select the finest valid
  interval producing at most 1,000 points. Reject an explicitly over-detailed
  combination rather than silently returning a huge response.
- Fill missing buckets explicitly, cap every response at 1,000 buckets and 20
  labels plus `other`, and distinguish unknown model/key and unknown
  cost from known zero.
- Return `requested_after`, `effective_after`, `before`, `bucket`, `retention_limited`,
  and earliest/latest retained completion timestamps. This lets the UI explain
  when `All retained` or another range is shortened by retention or empty data.
- Perform aggregation in SQLite, use query-plan tests, honor cancellation, and
  avoid loading individual request rows into Go memory.
- Reuse the global two-query analytics limiter and bounded 32-entry/15-second
  cache from T166. Cache keys contain only normalized range/dimension parameters.

Acceptance and tests:

- Tests cover UTC/day boundaries, leap day/DST-independent bucketing, sparse
  ranges, ties in top-N, null cost, deleted/unknown keys, invalid dimensions,
  missing latency samples, automatic bucket thresholds, explicit over-detailed
  requests, all-retained empty/large histories, retention gaps, and concurrent inserts.
- Sum of returned top rows plus `other` equals the untruncated aggregate for each
  metric where values are known. Bucket order and boundaries are deterministic.
- Responses are bounded and contain no raw key, body, full path/query, policy,
  credentials, or arbitrary high-cardinality dimensions.
- Representative one-year daily, 31-day hourly, and all-retained monthly queries
  meet a documented p95 target under 250ms and temporary allocation below 12 MiB
  per query without affecting proxy first-byte or stream-close delivery.

Reference: `docs/architecture/storage.md`,
`docs/architecture/accounting.md`.

Dependencies and out of scope: depends on T167. Do not add custom SQL, CSV export,
provider billing, timezone-specific server buckets, or materialized rollups.

### T169 - Build the Usage and Analytics page

Goal: provide an accurate, accessible analysis screen for traffic, tokens, cost,
models, keys, and outcomes.

Scope:

- Add URL-backed presets `1h`, `Today`, `24h`, `7d`, `30d`, `90d`, `1y`, and
  `All retained`, plus an accessible custom UTC range. Make `30d` a first-class
  visible preset rather than hiding it in a menu. Select five-minute/hour/day/
  week/month buckets automatically while allowing only valid overrides.
- Explain that `All retained` means all rows still available under the configured
  history-retention policy, not necessarily the gateway's lifetime. Show the
  effective first/last data timestamps and a retention-limited notice where
  applicable.
- Build a deliberate analytics composition rather than one generic chart:
  request volume as a line/area view with successful/error/rejected series;
  token consumption as a stacked area view for input/cached/output; estimated
  cost as a restrained area/line view; and average total/TTFB/upstream latency
  as a multi-line view with missing samples rendered as gaps, not zeros.
- Place those four metric views in one primary chart card controlled by clear
  `Requests`, `Tokens`, `Cost`, and `Latency` tabs. Render only the selected chart
  and keep the selected metric in the URL. Do not mount or paint all four charts
  simultaneously; this is the main simplification versus Bifrost.
- Add ranked horizontal bars plus a sortable accessible table for top models,
  keys, and outcomes. Show share of total, exact value, request count, and the
  bounded `other` aggregate. Use direct labels where they improve scanning and
  avoid pie/donut charts for close comparisons.
- Keep rankings in one secondary card with `Models`, `API keys`, and `Outcomes`
  tabs and one metric selector. Do not create separate ranking pages or repeat
  the same dataset in several chart types.
- Add summary cards with current total, previous-period delta where available,
  peak bucket, average rate, and freshness. Keep definitions available through
  concise tooltips/help text so tokens, cached tokens, cost, and latency cannot
  be misread.
- Include exact tooltips, units, legends, visible hover/focus markers, and a
  crosshair that follows the selected bucket. Tooltips stay within the viewport
  and can be reached without a pointer.
- Use Recharts behind project-owned wrappers. Keep chart colors semantic and
  distinguish series by more than color; provide an equivalent data table and a
  concise text summary for assistive technology.
- Defer heavy chart updates during rapid control changes, cancel obsolete
  requests, lazy-load the complete chart route, and perform no automatic Usage
  polling. Respect reduced motion.

Acceptance and tests:

- Charts handle all-zero, sparse, null-cost, large-value, single-bucket, and
  partial-retention datasets without false interpolation, clipped peaks, or
  invalid axes. Latency gaps and unknown cost are visibly distinct from zero.
- Keyboard users can operate controls and inspect the data table; screen readers
  receive range, units, freshness, and series meaning.
- The page is usable at 375px with no document-level horizontal overflow and at
  200% zoom without clipped controls/tooltips.
- The chart renders at most 1,000 points, creates no per-point DOM labels, stays
  responsive on a documented low-end browser profile, and meets route-chunk and
  DOM budgets.
- Visual regression fixtures cover dark/light themes, dense/sparse/extreme data,
  long labels, mobile layout, hover/focus tooltip, and color-vision-safe series.
  Gridlines, gradients, and fills remain subtle enough that data dominates.
- At 1440px the first viewport contains the range control, five or fewer KPI
  cards, and the primary chart; rankings follow below. At mobile widths KPI cards
  become a horizontal snap strip or compact grid and charts remain vertically
  readable without squeezing four panels side by side.
- Tests cover URL restoration, presets, invalid links, dimension/metric changes,
  1h/30d/all-retained bucket selection, retention notices, custom ranges, race
  cancellation, table/chart agreement, and empty/error states.

Reference: `docs/architecture/accounting.md`, UI screenshots in
`docs/ui/reference/` when present.

Dependencies and out of scope: depends on T168. Do not add forecasting, anomaly
detection, provider routing diagrams, realtime log streaming, saved reports,
CSV export, separate dimension-ranking pages, provider/model catalog, or Bifrost's
full advanced filter matrix.

## API Keys

### T170 - Build the API key inventory

Goal: let an operator quickly find, inspect, and understand existing gateway API
keys without exposing raw credentials.

Scope:

- Implement `/ui/keys` with cursor pagination, compact desktop table, mobile card
  layout, a maximum page size of 100, enabled/expired status, prefix,
  creation/expiry dates, and policy-summary
  indicators. Fetch only the current page and prefetch at most one next page.
- Add client-side search over the loaded page and filters for enabled/disabled,
  expiring, and policy-summary flags. Clearly label this as current-page filtering
  rather than implying a server-wide search.
- Add a key detail drawer/route shell using `GET /admin/v1/keys/:id`, with stable
  deep links and sections for identity, status, expiry, and policy summary.
- Preserve rows while paging/refetching and return focus predictably when a drawer
  closes. Never display or synthesize a raw key from its prefix.

Acceptance and tests:

- Empty, one-page, multi-page, expired, disabled, null-expiry, long-name, invalid
  cursor, deleted-between-pages, offline, and 401 states are covered.
- Pagination has no unbounded DOM/list accumulation and exposes position/status
  accessibly. Filters and status are not color-only.
- No raw key placeholder, fake reveal action, digest, full policy JSON, or admin
  credential appears in the DOM, URL, logs, or persisted browser state.
- Responsive and keyboard tests cover table, cards, pagination, row activation,
  detail deep link, and focus restoration.

Reference: `docs/operations/admin-api.md`,
`docs/architecture/policy.md`.

Dependencies and out of scope: depends on T169. Do not add server-side search,
sorting, key deletion, bulk actions, or usage-per-key analytics.

### T171 - Add API key creation and one-time secret handling

Goal: create keys safely from the UI and make the one-time credential handoff
clear enough that operators do not accidentally lose or leak it.

Scope:

- Add a focused create-key dialog/page using existing `POST /admin/v1/keys` with
  name and optional expiry, inline validation, explicit submit progress, and
  duplicate-submit prevention.
- Show the returned raw key exactly once in a blocking success step with copy and
  download actions, a visibility toggle, and confirmation that the operator has
  stored it before dismissal.
- Keep the raw key only in component memory. On dismissal, logout, route teardown,
  or session expiry, clear references and make the secret unrecoverable without
  creating another key. Mark the secret region to reduce accidental capture by
  password managers/autofill where standards allow.
- Refresh inventory data only after creation succeeds; errors must not close the
  form or invent a key state.

Acceptance and tests:

- The raw key never enters URL/history, query cache, web storage, analytics,
  console/error logs, clipboard without an explicit action, or a service worker.
- Copy success/failure, download, keyboard flow, visibility, cancellation before
  submit, conflict/validation/5xx, double click, and expiry are tested.
- Closing the one-time step requires explicit acknowledgement and warns that the
  key cannot be shown again; revisiting the created key shows metadata only.
- The downloaded file is plain text with a safe generated filename and no other
  credentials or page content.

Reference: `docs/operations/admin-api.md`,
`docs/architecture/operations.md`.

Dependencies and out of scope: depends on T170. Do not add import, rotation,
batch creation, key recovery, deletion, or automatic clipboard writes.

### T172 - Build the key policy editor and status controls

Goal: let operators inspect and atomically replace a key's effective policy
without requiring them to edit raw JSON.

Scope:

- Complete the key detail route with a structured form for enabled state,
  allowed/denied model patterns, request/token windows, token mode, concurrency,
  budget limits, body logging flags, and expiry metadata (read-only if unsupported
  by the current API).
- Keep inventory, create-secret handoff, policy form, and key-detail composition
  as separate modules behind the API Keys feature entry; one workflow must be
  testable without mounting or importing all others.
- Convert seconds and integer micro-dollars into clearly labeled human inputs
  without losing exact values on round-trip. Support add/remove/reorder for list
  fields and distinguish omitted/unlimited from explicit zero.
- Submit one full replacement through `PUT /admin/v1/keys/:id/policy`, include a
  review summary for security-sensitive changes, and warn before leaving a dirty
  form. Use the server response as the new source of truth.
- Explain policy semantics inline, especially allow-vs-deny precedence, body
  capture sensitivity, budgets, and disabling a key. Do not hide advanced fields
  behind undocumented defaults.

Acceptance and tests:

- A complex policy loads and saves without semantic or numeric drift. Null,
  omitted, zero, maximum values, duplicate patterns, and invalid ranges are tested.
- Validation is associated with fields, summarized on submit, and moves focus to
  the first error. A 409 preserves edits and gives a safe retry path.
- Concurrent server changes are not silently overwritten after a stale refetch;
  the user sees a conflict/reload decision where the API can identify it.
- Keyboard, responsive, dirty-navigation, disable/re-enable, error, and successful
  mutation/cache-refresh flows are covered.

Reference: `docs/architecture/policy.md`,
`docs/operations/admin-api.md`.

Dependencies and out of scope: depends on T171. Do not add policy templates,
JSON mode, key deletion, expiry mutation, bulk editing, or speculative API fields.

## Request Inspection

### T173 - Build the request history explorer

Goal: provide a fast, shareable, and readable request history view over the
existing bounded metadata API.

Scope:

- Implement `/ui/requests` with cursor pagination and URL-backed filters for key,
  completion range, and page size capped at 100. Add accessible presets plus exact RFC3339/UTC
  handling; use the key list for a bounded selector.
- Present request ID, key, method/route, model, status/outcome, modes, tokens,
  estimated cost, duration, and completion time in a dense desktop table and a
  deliberate mobile representation.
- Add outcome/status badges, null-vs-zero display, sortable-looking headers only
  where sorting actually exists, row deep links, copy-request-ID action, and
  contextual empty states.
- Preserve current results during page/filter transitions, cancel obsolete
  queries, and keep opaque cursors out of shareable URLs.

Acceptance and tests:

- Tests cover empty and 1000+ histories, multiple pages, combined filters,
  invalid/deleted bookmarks, unknown keys/models, every outcome, null usage,
  long values, 401, offline, and backend errors.
- Page boundaries have no UI duplicates, accidental accumulation, or focus loss.
  New incoming requests do not unexpectedly reorder a page being inspected.
- The UI does not imply unsupported model/status/search/sort filters and never
  exposes bodies, headers, raw keys, queries, or SQL in the list.
- Filters are keyboard accessible, survive a valid copied link, and collapse
  cleanly on narrow screens without horizontal document overflow.

Reference: `docs/operations/admin-api.md`,
`docs/architecture/observability.md`.

Dependencies and out of scope: depends on T172. Do not add real-time tail,
full-text search, replay, export, arbitrary sorting, or server API expansion.

### T174 - Build request details and the safe body viewer

Goal: make one request diagnosable from metadata and captured bytes while
preserving the API's exact-byte and secret-safety guarantees.

Scope:

- Implement `/ui/requests/:id` with identity, route/model, statuses/outcome,
  streaming modes, byte/token/cost values, timeline/latencies, error metadata,
  and links back to the preserved history filters.
- Isolate metadata sections, timeline, body fetch/preview, and exact-byte download
  so normal request details do not load body-viewer code until explicitly opened.
- For available body kinds, fetch only on explicit user action. Show original
  size/truncation warnings and provide raw text/hex-safe preview plus exact-byte
  download. JSON pretty view may be derived client-side but raw bytes remain the
  authoritative representation.
- Bound preview decoding/rendering, handle binary/NUL/invalid UTF-8 safely, and
  never inject body content as HTML. Do not syntax-highlight through unsafe HTML
  or send content to third-party services/workers.
- Render at most the first 256 KiB in text/hex preview, with an explicit download
  path for exact full captured bytes. Do not keep closed body tabs mounted.
- Treat captured bodies as sensitive: no query caching beyond the mounted view,
  no persistence, no automatic clipboard, no rendering in error reports, and
  clear memory references on close/navigation/logout as far as browser APIs allow.

Acceptance and tests:

- Detail tests cover successful, rejected, cancelled, upstream-error, converted
  SSE, null fields, all body combinations, no bodies, 404, and concurrent body
  retention deletion.
- Body tests cover JSON, SSE fragments, gzip bytes, binary, invalid UTF-8, empty,
  maximum capture, truncation, download equality, and malicious HTML/script text.
- Exact downloaded bytes and server metadata match the body endpoint. Preview
  limits prevent a large body from freezing layout or blocking navigation.
- Tabs/disclosures, copy actions, timeline, warnings, and back navigation are
  keyboard/screen-reader accessible and responsive.

Reference: `docs/operations/admin-api.md`,
`docs/architecture/observability.md`.

Dependencies and out of scope: depends on T173. Do not add replay, body search,
server-side formatting/decompression/redaction, editing, or diffing.

## Operations and Quality

### T175 - Add a safe admin system information API

Goal: expose enough bounded runtime information for a useful System screen
without leaking configuration secrets or turning the UI into a control plane.

Scope:

- Add authenticated `GET /admin/v1/system` returning version/commit/build time,
  uptime/start time, readiness check results, SQLite schema/health, telemetry
  queue depth/capacity and drop counters, active requests, and safe retention/
  body-capture limits.
- Return only explicitly allowlisted fields. Do not serialize the config struct,
  environment, upstream URL/userinfo, filesystem paths, credentials, peppers,
  pricing rules, raw metric labels, host identity, or database contents.
- Reuse readiness/metrics sources without scraping the gateway over HTTP. Bound
  checks with context and ensure a slow detail cannot hold up proxy transport.
- Define whether each field is snapshot, monotonic, nullable, or unavailable and
  keep the response stable enough for the typed frontend contract.

Acceptance and tests:

- Healthy, degraded, shutdown, closed-storage, saturated-telemetry, dev-build,
  and unavailable-field states return truthful bounded responses.
- Canary tests prove secrets and unsafe config fields cannot appear even when
  their values are injected into every config source.
- Endpoint requires admin authentication, honors cancellation, and does not
  change public `/health`, `/ready`, or `/metrics` behavior.
- Race and latency tests cover concurrent runtime changes without introducing
  locks/contention on request hot paths.

Reference: `docs/architecture/operations.md`,
`docs/architecture/observability.md`.

Dependencies and out of scope: depends on T174. Do not add config editing,
restart/shutdown buttons, SQL access, log streaming, environment display, or
provider health checks.

### T176 - Build the System and diagnostics page

Goal: give operators a clear read-only view of gateway readiness, build identity,
runtime pressure, storage, telemetry, and relevant safety limits.

Scope:

- Implement `/ui/system` using T175 with overall health, individual readiness
  checks, build/runtime details, active request and telemetry indicators, storage
  state, retention/body limits, freshness, and a manual refresh action.
- Explain degraded states and link to relevant local documentation without
  exposing internals. Clearly distinguish unavailable, unknown, zero, warning,
  and failure states using icon, text, and color.
- Add a copyable diagnostics summary containing only the endpoint's allowlisted
  non-secret fields and an explicit preview of exactly what will be copied.
- Poll no faster than every 60 seconds and only while visible; stop on navigation/logout and preserve the last
  snapshot with a stale marker during transient failures.

Acceptance and tests:

- Healthy/degraded/offline/shutdown-like/stale/dev-version/queue-pressure states
  are understandable without relying on color or implementation jargon.
- Diagnostics copy contains no admin/session/raw API key, URL query, body, path,
  environment, upstream target, browser storage, or hidden DOM data.
- Page works on mobile, at 200% zoom, with keyboard/screen reader, and with long
  version/check messages.
- Tests cover polling lifecycle, refresh races, copy preview, null fields, errors,
  and links; snapshot data cannot survive logout in query cache.

Reference: `docs/architecture/operations.md`,
`docs/operations/deployment.md`.

Dependencies and out of scope: depends on T175. Do not add settings mutation,
remote restart, terminal, raw Prometheus browser, SQL console, or log viewer.

### T177 - Complete global UX states and interaction polish

Goal: make the console feel coherent under normal, slow, empty, stale, offline,
and failed conditions rather than polished only on the happy path.

Scope:

- Standardize route loading, background refresh, empty state, inline error,
  offline, stale data, mutation progress, success feedback, destructive warning,
  and session-expired patterns across all pages.
- Add an accessible command/search palette for navigation and safe global actions
  only: go to page, toggle theme, refresh current page, and logout. Do not index
  request bodies, API records, key names, or server data.
- Add breadcrumbs/back behavior, copy feedback, unsaved-change guards, focus
  restoration, scroll restoration, and consistent keyboard shortcuts that never
  fire while typing in a field.
- Use subtle 150-250ms transitions only where they communicate state/spatial
  continuity; avoid ornamental chart/card animation and honor reduced motion.

Acceptance and tests:

- No page shows a blank screen during loading/error, replaces usable stale data
  with a spinner, or emits duplicate toasts for one action.
- Global shortcuts are discoverable, remappable only if later required, and do
  not conflict with browser/assistive technology conventions.
- Focus moves predictably after navigation, dialogs, errors, pagination, and
  mutations. Route errors can recover without a full browser reload when safe.
- Cross-page interaction tests cover slow network, offline/online transition,
  401 during mutation, duplicate actions, reduced motion, and rapid navigation.

Reference: `docs/ui/design-system.md`.

Dependencies and out of scope: depends on T176. Do not add server-side search,
notifications inbox, onboarding tour, user preferences API, or decorative motion.

### T178 - Pass accessibility and responsive design gates

Goal: independently harden every implemented screen for keyboard, assistive
technology, zoom, contrast, touch, and real viewport constraints.

Scope:

- Audit Login, Overview, Usage, Keys, key create/policy, Requests, request detail,
  body viewer, System, navigation, dialogs, tables, charts, and error states
  against WCAG 2.2 AA.
- Add automated axe checks and focused manual-test documentation for landmarks,
  headings, names/roles/values, tab order, focus visibility/not-obscured, dialog
  behavior, live regions, table semantics, chart alternatives, and form errors.
- Verify 320/375/768/1024/1440 widths, portrait/landscape, touch targets, 200% and
  400% zoom/reflow, long localized-looking strings, safe areas, forced colors,
  dark/light contrast, and reduced motion.
- Fix findings in the owning components with the smallest correct changes; do
  not create a parallel accessibility-only component set.

Acceptance and tests:

- Automated scans have zero serious/critical violations and no ignored rules
  without a documented, reviewed reason.
- All functionality is operable keyboard-only with visible, unobscured focus and
  no trap except an open modal. Status/errors are announced once and meaningfully.
- There is no accidental document horizontal scrolling at required widths/zoom;
  intentional data-region scrolling is labeled and keyboard reachable.
- `docs/ui/accessibility.md` records the test matrix, manual results, exceptions,
  and exact commands so the gate is repeatable.

Reference: `docs/ui/design-system.md`,
`docs/architecture/testing.md`.

Dependencies and out of scope: depends on T177. Do not redesign product scope,
add localization, claim formal certification, or waive issues for visual parity.

### T179 - Add end-to-end security, browser, and performance coverage

Goal: prove the complete Web UI works against a real gateway and remains safe
under authentication, navigation, data, and failure edge cases.

Scope:

- Add Playwright tests against a real gateway, real SQLite, and controlled
  upstream covering login/logout/expiry, Overview/Usage, key create and policy
  update, request filtering/detail/body download, System, deep links, and reloads.
- Run representative Chromium coverage plus a smaller Firefox/WebKit smoke set.
  Test desktop/mobile projects, dark/light themes, reduced motion, and browser
  back/forward behavior.
- Add web-specific security tests for CSP, framing, MIME sniffing, CSRF, cookie
  flags, credential/secret persistence, XSS through names/models/errors/bodies,
  unsafe downloads, cache after logout, and cross-origin requests.
- Measure production bundle/chunks, initial route rendering, large table/chart
  interaction, DOM size, layout shift, idle CPU/network use, and frontend impact
  on gateway startup/memory. Enforce the milestone budgets in CI.

Acceptance and tests:

- Critical journeys pass without arbitrary sleeps, production-only bypasses, or
  mocked core gateway APIs; fixtures are deterministic and cleaned up.
- No raw/admin/session credential or captured body survives logout in storage,
  query cache, service workers, logs, screenshots, traces, or test artifacts.
- Initial JS, route chunks, CLS, and interaction budgets are documented and met;
  idle pages generate no work beyond their documented visible-tab polling; a UI
  build does not alter proxy first-byte, per-chunk, or stream-close behavior.
- On a documented low-end mobile emulation, primary routes remain interactive
  without long tasks over 200ms; gateway RSS growth from embedded assets plus 32
  cached analytics responses stays below 32 MiB in the representative test.
- CI commands fail clearly on frontend unit, accessibility, E2E, security, stale
  embedded assets, or bundle-budget regressions.
- Import-boundary checks pass, route chunks do not accidentally absorb unrelated
  features, and a dependency report documents any shared module imported by most
  routes so architectural creep is visible.

Reference: `docs/architecture/testing.md`,
`docs/architecture/transport.md`, `docs/architecture/operations.md`.

Dependencies and out of scope: depends on T178. Do not add synthetic production
monitoring, third-party analytics, load testing infrastructure, or visual SaaS.

### T180 - Integrate, document, and release the Web UI milestone

Goal: ship the complete console as part of the normal binary/Docker workflows
with reproducible builds, operator documentation, and final visual review.

Scope:

- Integrate the web build into Docker and release workflows with pinned Node/npm
  inputs, deterministic assets, layer caching, and no Node runtime/dev sources in
  the final image. Preserve non-root execution and the existing image-size goal
  unless measured assets require a documented revision.
- Update README and operations docs with UI URL, development/build commands,
  browser-session behavior, reverse-proxy HTTPS requirements, supported browsers,
  troubleshooting, accessibility notes, and every UI-visible API addition.
- Capture approved desktop/mobile dark/light screenshots in `docs/ui/` and perform
  final visual review for hierarchy, spacing, overflow, empty/error states,
  contrast, icon consistency, and fidelity to the design system/reference intent.
- Audit T161-T180 acceptance criteria, dependency/license notices, production
  source maps, CSP, asset cache headers, stale generated assets, and clean-checkout
  build reproducibility. Update `CURRENT.md` to mark the milestone complete.

Acceptance and tests:

- A clean checkout can build the web assets, Go binaries, and Docker image; one
  container serves proxy/admin/operations endpoints and the functional `/ui/`.
- The documented quickstart reaches login, creates a key, sends a request, sees
  usage/history, inspects it, and logs out without requiring frontend tooling.
- All frontend and Go checks, race tests, Playwright projects, accessibility gate,
  bundle budget, secret canaries, and documentation-link checks pass.
- Final image contains no npm cache, source maps, test credentials, Playwright,
  development server, or writable web assets. Existing gateway invariants remain
  covered and green.

Reference: `docs/architecture/operations.md`,
`docs/operations/deployment.md`, `docs/architecture/testing.md`.

Dependencies and out of scope: final task in T161-T180; depends on T179. Do not
add provider routing UI, multi-user RBAC, config editing, replay/export, WebSocket
tailing, plugins, or T181+ features.

---

## Milestone Summary

After completing T161-T180, 9Gateway provides:

**Embedded Console:**
- React/TypeScript UI served from the single Go binary and Docker image
- Responsive dark/light operations shell with deep links and accessible primitives
- Secure short-lived browser sessions without persistent admin credentials

**Operations and Analytics:**
- Overview with health, traffic, token, cost, key, and recent-request summaries
- Previous-period comparisons plus bounded request/outcome, stacked-token,
  estimated-cost, and latency/TTFB time-series visualizations
- Ranked model/key/outcome bars and accessible data tables with exact tooltips
- Read-only System diagnostics built from explicitly allowlisted runtime data

**Key and Request Workflows:**
- Key inventory, one-time key creation, and structured policy/status editing
- Paginated request history with shareable filters and complete metadata details
- Sensitive, bounded body preview and exact-byte download on explicit demand

**Quality and Delivery:**
- WCAG 2.2 AA-oriented keyboard, screen-reader, zoom, contrast, and mobile coverage
- Unit, contract, browser E2E, security, and performance/bundle regression gates
- Reproducible embedded/Docker builds and complete operator/developer documentation

**Explicitly Not Included:**
Provider routing or topology, protocol translation, multi-user RBAC, configuration
editing, key deletion/rotation, request replay/export, real-time tailing, plugins,
Redis/PostgreSQL, or a separate frontend deployment.
