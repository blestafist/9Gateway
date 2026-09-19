# Current Work

Current milestone: embedded Web UI (`T161`-`T180`) complete.

Done: `T001`-`T180`.

Current: none.

Queued: none.

T179 completed end-to-end security, browser, and performance coverage:
added Playwright journeys for authentication, protected deep links, API-key lifecycle, request body inspection, diagnostics, reload/back-forward, and logout/cache scrubbing; added security checks for CSP, framing/content sniffing, HttpOnly/SameSite cookies, CSRF, XSS, and credential persistence; added DOM, CLS, idle-network, long-task, bundle, dependency-boundary, proxy hot-path, and RSS-growth budgets; verified Chromium, light Chromium, Mobile Chromium, and Firefox; documented the WebKit host-library block and repeatable verification matrix in `docs/ui/budgets-and-verification.md`; added CI gates for Go and frontend quality/budget/E2E checks.

T178 passed accessibility and responsive design gates:
audited all console routes and modals (Login, Overview, Usage, Keys inventory, Create Key,
Key Policy drawer, Requests explorer, Request Details, Body Viewer, System diagnostics,
Command Palette, and Route Error Fallbacks) against WCAG 2.2 Level AA;
integrated automated axe-core accessibility scanner suite (`web/src/tests/a11y.test.tsx`)
enforcing zero serious or critical accessibility violations across all mounted screens,
modals, drawers, and composable UI primitives;
fixed BodyViewer dynamic tabpanel referencing where tabs only bind `aria-controls` to
rendered active panels, eliminating broken ARIA pointers;
added labeled, keyboard-reachable scroll regions (`role="region"`, `tabIndex={0}`,
`aria-label`) to `Table`, `BodyViewer` text/hex previews, and `DiagnosticsSummaryModal`;
removed redundant nested table wrappers in `RequestTable`;
added accessible labels to the Command Palette combobox input;
hardened design tokens in `tokens.css` with dedicated Windows High Contrast Mode
`@media (forced-colors: active)` border and focus ring rules, and `--safe-area-*` tokens;
verified responsive reflow across 320px, 375px, 768px, 1024px, and 1440px viewports with
`overflow-x: hidden` preventing accidental document horizontal scrollbars;
documented repeatable automated and manual test procedures and matrix in `docs/ui/accessibility.md`.

T177 completed global UX states and interaction polish:
standardized route loading, background refresh, empty state, inline error, offline, stale data,
mutation progress, success feedback, destructive warning, and session-expired patterns across
all console routes; implemented an accessible, responsive Command & Search Palette (`Ctrl+K` / `⌘K`)
featuring in-memory search and keyboard navigation over page destinations, theme toggle, current page
query refresh, and session logout with zero indexing or DOM leakage of server data, keys, or payload bodies;
added discoverable global shortcuts with strict typing-field guards preventing execution while editing
text or forms; implemented client-side route error boundary recovery without full browser reloads;
added scroll restoration to top and screen-reader focus restoration to `#main-content` upon route changes;
added duplicate toast suppression within debounce windows; integrated online/offline status monitoring
into shell status indicators and page alerts; added hierarchical breadcrumbs with direct parent navigation
for `/requests/:id` and `/keys/:id`; verified by isolated unit, shell, lifecycle, and cross-page interaction tests.

Review fixes for T171-T175:
addressed review findings across admin system telemetry & readiness constructor semantics,
custom key expiry UTC datetime-local handling and validation, RequestDetailView direct
download lifecycle/error feedback, BodyViewer view mode fallback, request-range bookmark
validation, duplicate policy submits, and lossless int64 policy handling with unsafe numeric
values rejected at the UI contract boundary.

T176 built the System and diagnostics page:
connects `GET /admin/v1/system` into an accessible, responsive diagnostics dashboard below
`/ui/system`; presents overall readiness badge, individual readiness checks (`sqlite`,
`schema`, `telemetry`, `upstream`, `lifecycle`) with failure messages and context-safe
documentation links, build/runtime indicators (version, commit, build time, start time, uptime),
telemetry queue pressure and drops with active request gauges, storage status with schema
version comparison and mismatch alert, and operational retention and body-capture limits;
clearly distinguishes null, unknown, zero, warning, and failure states with icon, text, and color;
provides a safe diagnostics summary modal displaying an exact allowlisted non-secret text
preview and copy-to-clipboard action with toast confirmation; implements visible-tab adaptive
polling (>= 60s) pausing on document hidden or offline events, manual refresh deduplication
preventing concurrent queries, and stale snapshot preservation with warning alert on background
refresh failure; ensures zero leakage of secrets or internal paths in DOM, URL, or diagnostics text,
and query cache scrubbing on logout; verified by unit, component, lifecycle, navigation, and contract tests.

T175 added a safe admin system information API:
connects authenticated `GET /admin/v1/system` returning version, commit, build time,
uptime, start time, readiness check results, SQLite schema and health, telemetry
queue depth/capacity and drop counters, active requests, and safe retention and
body-capture limits; enforces strict allowlist-only serialization ensuring credentials,
auth pepper, filesystem paths, hostnames, upstream credentials, database contents,
environment variables, and raw metric labels are never leaked; reuses in-process readiness
and metrics sources without HTTP self-scraping; bounds execution with context cancellation
and timeouts; keeps public `/health`, `/ready`, and `/metrics` unaffected; verified by
authentication, method, parameter validation, healthy, degraded, shutdown, saturated
telemetry, canary secret leak, cancellation, race, and latency tests.

T174 built request details and the safe body viewer:
connects `GET /admin/v1/requests/:id` and `GET /admin/v1/requests/:id/bodies/:kind` into
a detailed, accessible request inspection view below `/ui/requests/:id` with back-navigation
preserving URL filter state; renders complete request identity, API key, route, model,
status code, terminal outcome badges, streaming mode, timing and latency waterfall timeline,
token usage, and cost with strict preservation of null vs explicit zero; lazy loads the
`BodyViewer` component only upon explicit operator disclosure/action; previews available
request, response, or stream-aggregation bodies up to 256 KiB with text and hex views,
original capture size indicators, and truncation warnings; handles binary, NUL bytes,
gzip compression, SSE fragments, invalid UTF-8, and malicious markup safely without
HTML injection or remote leaks; offers exact-byte download matching authoritative
server payload bytes; guarantees captured bodies are never cached in TanStack query cache
beyond the mounted view, never persisted to web storage or session history, and scrubbed
upon navigation or unmount; verified by unit, component, lifecycle, navigation, and contract tests.

T173 built the request history explorer:
connects `GET /admin/v1/requests` into a dense, accessible request explorer below
`/ui/requests` featuring cursor pagination with bounded page sizes (10, 25, 50, 100;
default 25), single-next-page prefetching, and zero URL/history cursor leakage;
URL-backed filters for API key, completion range presets (all retained, 1h, 24h, 7d, 30d,
and custom UTC with exact RFC3339 validation), and page size; bounded key selector
using the existing admin key list with graceful handling for unknown or deleted key bookmarks;
dense desktop table and intentional mobile card views presenting request ID, API key,
method and route, model, non-color-only status and outcome badges, modes, token counts,
estimated costs, microsecond durations, and completion timestamps; strict preservation of
null vs explicit zero for token counts, estimated costs, and durations; non-sortable table
headers preventing misleading sort affordances; explicit copy-ID action with polite
screen-reader announcements and clipboard feedback; row deep links pointing to `/requests/:id`
with route integration; contextual empty states for initial history, filtered keys, and
filtered time ranges; honest handling of 401 session expiration, offline network connectivity,
backend 500 errors, and 400 invalid parameter/cursor expiration with first-page restart;
verified by unit, component, lifecycle, navigation, and contract tests.

T172 built the key policy editor and status controls:
connects `PUT /admin/v1/keys/:id/policy` into a structured, responsive policy editor
within the key detail route below `/ui/keys`; cleanly separates inventory (`KeyInventory`),
creation secret handoff (`CreateKeyDialog`), policy editor (`KeyPolicyForm`), and key detail
composition (`KeyDetailDrawer`) behind the API Keys feature entry (`web/src/features/keys/index.ts`);
provides exact lossless round-trip conversions between seconds and human units (s, m, h, d)
and between integer micro-dollars and USD with up to 6 decimal places (and µ$ unit); supports
dynamic adding, removing, and reordering for allowed models, denied models, request windows,
token windows, and budget limits; cleanly distinguishes unlimited/omitted from explicit zero;
explains policy semantics inline including allow-vs-deny precedence, payload body logging
sensitivity, budget resets and reservations, and immediate key disabling; performs field-level
validation with submit summary alert and automatic focus on the first errored field; provides
a security-sensitive review summary confirmation modal before submitting high-impact changes;
warns before discarding dirty edits via beforeunload and modal dialog; preserves unsaved edits
on 409 conflict with safe retry; detects concurrent server modifications from background refetches
with an explicit conflict/reload decision; updates TanStack query cache using the server response
as the single source of truth; verified by unit, component, lifecycle, validation, and contract tests.

T171 added API key creation and one-time secret handling:
connects `POST /admin/v1/keys` into an accessible, focused creation dialog below `/ui/keys`;
features inline name and expiration validation (with preset durations and custom UTC datetime enforcement),
explicit submit progress, duplicate-submit prevention via ref locks and button disabling, and preservation
of form inputs on conflict/validation/5xx server errors; returns raw secrets exactly once inside a blocking
success step with masked password default, visibility toggle, explicit copy action with polite screen-reader
announcements and error feedback, safe plain-text download (`9gateway-key-<name>-<prefix>.txt` containing only
the raw secret), anti-autofill markers (`data-1p-ignore`, `data-lpignore`, `data-bwignore`, `data-form-type="other"`),
and mandatory acknowledgement checkbox before dismissal; guarantees raw secret retention strictly in component
memory with immediate scrubbing on modal dismissal, user logout, session expiry, or route unmount; ensures zero
leakage into TanStack query cache, URLs/history, web storage, console/error logs, or clipboard without an explicit click;
refreshes inventory data only on successful creation; verified by behavioral, validation, lifecycle, and contract tests.

T170 built the API key inventory:
connects `GET /admin/v1/keys` into an accessible, responsive key inventory below
`/ui/keys` featuring in-memory cursor pagination with bounded page sizes (10, 25, 50,
100; default 25), single-next-page TanStack prefetching, and zero URL/history cursor
leakage; client-side searching and filtering by name, key ID, display prefix, status
(active, disabled, expired, expiring <= 30d), and policy summaries (model allowlist/
denylist, body logging) clearly scoped to current-page filtering; deep-linkable
key detail drawer shell (`/ui/keys/:id` or `?keyId=:id`) using `GET /admin/v1/keys/:id`
with stable focus restoration to activated rows or cards on dismissal, handling
404 deleted keys; dual presentation with compact desktop table and mobile cards
without color-only status indicators; honest error, offline, invalid cursor, and
401 session expiry handling; strict DOM safety ensuring raw keys or reconstructed
secrets never appear in DOM, URL, or client logs; verified by unit, integration,
lifecycle, and contract tests.

T169 built the Usage and Analytics page:
connects the T168 usage time-series and breakdown APIs into URL-backed presets
(`1h`, `Today`, `24h`, `7d`, `30d`, `90d`, `1y`, `All retained`, `Custom UTC`) with `30d` as
a first-class visible preset and accessible custom UTC datetime range validation;
dynamic bucket resolution restriction (`auto`, `five_minutes`, `hour`, `day`, `week`, `month`)
preventing over-detailed combinations; retention explanation explaining `All retained`
as rows available under configured retention policy with first/last timestamps and
warning notice when retention-limited; five KPI cards (total requests, total tokens,
estimated cost, average latency, errors & rejections) with honest previous-period deltas
labeled as unavailable when not cleanly derivable; lazy single-metric primary chart card
switching between Requests, Tokens, Cost, and Latency views with URL synchronization and
Recharts project wrappers; accessible alternative data table with full time-window breakdown
and live screen-reader summaries; secondary rankings card with Models, API Keys, and
Outcomes dimension tabs and Requests, Tokens, and Cost metric selectors, horizontal percentage
fill bars, direct labels, unknown/deleted key badges, bounded `other` aggregate row, and
sortable detailed table; responsive desktop (1440px) and mobile (375px) layouts without
horizontal overflow; TanStack queries with query cancellation, keepPreviousData, and
no automatic polling; verified by unit, integration, and lifecycle tests.

T168 added the authenticated usage time-series and breakdown APIs:
`GET /admin/v1/usage/timeseries` and `GET /admin/v1/usage/breakdown` support
RFC3339 range filtering with optional `after` (omitting requests all retained history
without conflating deleted past records), safe bucket resolutions (`five_minutes`
up to 24h, `hour` up to 31d, `day` up to 2y, `week` up to 10y, `month` for longer or
all-retained, and `auto` finest resolution producing <= 1,000 points); over-detailed
combinations producing > 1,000 buckets are rejected with HTTP 400; missing buckets are
filled explicitly; nullable cost and average total/TTFB/upstream latencies include sample
counts to distinguish absent observations from zero; breakdown supports top-20 ranking
by model, key, and outcome with deterministic tie-breaking, safe identifiers,
unknown/deleted key indicators, and an `other` aggregate strictly matching untruncated
totals; `retention_limited` flag and earliest/latest retained completion timestamps explain
retention boundaries honestly; index-backed SQLite scans (`idx_requests_finished`)
meet p95 < 250ms latency and < 12 MiB memory budgets; global two-query analytics
concurrency limiter (HTTP 503 with Retry-After: 1), singleflight deduplication, and
bounded 32-entry/15-second LRU caching prevent upstream transport disruption; zero
credential, body, or arbitrary high-cardinality dimension leakage.

T167 built the operational Overview page:
connects the T166 admin overview aggregation API into a period preset selector
(1h, 24h, 7d, 30d), operational KPI cards (total/active requests, input/cached/output
tokens, estimated cost, error and rejection counts), previous-period deltas with
direction and exact comparison text (neutral for volume/tokens/cost, warning for
error/rejection increases, explicit estimate labels for cost, honest placeholders
for unknown/null baselines), gateway health and operations strip, key configuration
summary, recent requests list with long-identifier truncation, and deep navigation
links to Usage, Keys, Requests, and System screens; adaptive polling (minimum 30s)
that pauses when the tab is hidden or offline, with manual refresh deduplication
and stale snapshot preservation on background refresh failure; honest empty,
partial-data, loading skeletons, capacity constrained (503), and expired session (401)
states with login recovery; verified by isolated component and lifecycle unit tests.

T166 added the authenticated admin overview aggregation API (`GET /admin/v1/overview`):
RFC3339 range query validation with default 24h window and bounded ranges up to
one year; aggregated request counts, terminal outcomes (complete/custom_dispatch
successes, error outcomes, pre_upstream rejections), nullable token/cost sums
preserving unknown vs zero distinction, active request gauge, total and enabled key
counts, and up to 10 recent request summaries; exact adjacent non-overlapping
previous comparison range computed in SQLite with index-backed scans; safe global
concurrency bounding (max 2 concurrent aggregation queries across gateway with
retryable HTTP 503 and Retry-After: 1), in-flight singleflight query deduplication,
and LRU caching for up to 32 completed queries with a 15-second TTL; request
cancellation propagation without caching aborted computations; zero credential,
policy JSON, or request/response body leakage in overview responses.

T165 implemented the typed admin data layer:
bounded typed HTTP transport with SameSite CSRF handling, generation barriers for
superseded queries and logout cache purges, concurrency-bounded request scheduling
(max 4 concurrent reads, 1 concurrent mutation), safe idempotent GET retry policy
(never retrying mutations or offline states), TanStack Query configuration with
cache clearing on auth transitions, feature-owned runtime response validation and
fixtures for Keys, Requests, and System APIs, robust telemetry formatters for
durations, costs, tokens, and bytes, and cursor pagination helpers that exclude
opaque cursors and credentials from browser history and shareable URLs.

T164 implemented secure browser session management and the operator login flow.
The gateway issues cryptographically random, bounded, in-memory sessions with
idle and absolute expiry, mapped to an opaque HttpOnly SameSite=Strict cookie
scoped to `/admin` (`/admin/ui/v1/session` and `/admin/v1/*`, excluding `/v1/*` proxy
and `/ui/*`). Secure derives from direct TLS or trusted reverse proxies
(`GATEWAY_TRUSTED_PROXIES`). Sessions rotate upon login and revoke on explicit
logout, expiry, or gateway process restart. Admin Bearer authentication remains
unaffected, while cookie-authenticated mutations require strict same-origin validation
and session-bound CSRF tokens. Failed logins are rate-limited via a bounded
in-memory limiter. The frontend provides accessible login, protected route redirection
with returnTo recovery, explicit logout, and zero persistence or DOM leakage of
credentials or session identifiers.

T163 built the responsive operations console shell and routing below `/ui/`
using React Router with lazy feature entries for Overview, Usage, API Keys,
Requests, System, Login, and Not Found. Navigation is strictly limited to
9Gateway operations (Overview, Usage, API Keys, Requests, System), excluding
unsupported 9router provider/routing features. Shell features desktop sidebar,
compact collapsed mode with tooltips, mobile navigation drawer with Escape/focus
handling, page header, breadcrumbs, `aria-current="page"` semantics, skip link
targeting `#main-content`, and reserved connection/version indicators.
Only theme and sidebar collapsed preference are stored in localStorage; no
credentials or secret telemetry are persisted. Per-route loading skeletons and
error boundaries preserve shell stability during lazy chunk arrival or failure,
and the development component catalog remains tree-shaken from production builds.

T162 established the operations-console visual design system and composable UI
primitives in `web/src/shared/ui` and `docs/ui/design-system.md`. The visual
direction uses charcoal/slate surfaces, restrained warm-coral brand accents,
semantic telemetry roles (neutral requests, coral input tokens, blue cached
tokens, green output tokens, and amber cost derived from reference screenshots),
a subtle CSS grid background texture, and compact density. Primitives include
Button, IconButton, Input, Select, Checkbox, Switch, Badge, Card, Tabs, Tooltip,
Dialog, Drawer, Table shell, Skeleton, EmptyState, Alert, and Toast region with
polite/assertive live-region semantics. A dev-only component catalog is
accessible at `/ui/components` and tree-shaken from production builds. Focus
trapping/restoration, high-contrast mode, reduced-motion, and theme bootstrapping
without flash are verified by unit tests.

The gateway now provides complete admin read API, CLI tool, health/metrics endpoints, graceful shutdown, security hardening, and production packaging.
It also provides transparent policy enforcement, token and budget accounting,
bounded safe request tracing, optional per-key body capture, structured
completion logging, persistent request history with independent retention, and
ordered lifecycle shutdown. Detailed telemetry is best effort and droppable;
transport and critical accounting remain independent of its queues and sinks.
Independent review fixes: transport, storage, startup, metadata, performance,
and coverage-checker findings are closed; the aggregate core-package coverage
gate is machine-enforced.
Provider routing/translation, tool-call execution/validation, Redis, and
PostgreSQL remain out of scope. The active Web UI milestone covers only
9Gateway-owned operations: health, usage, keys, requests, captured bodies, and
safe runtime diagnostics.

Review fixes: history shutdown now forms a SQLite-close completion barrier,
body captures transfer immutable ownership without defensive re-cloning,
retention passes are capped at 1000 rows while T140 verifies log/persistence
scalars, policy bodies, and restart retention. Storage review fixes for T136-T139:
lifecycle timestamp ordering constraints, indexed retention by completion time,
and an enforced schema/config body-size compatibility constant (schema version 9).
Metrics review fix: early URI/query and declared body-size ingress rejections
now increment the primary request counter exactly once, and bounded
`gateway_build_info` exposes safe build metadata labels.
Transport review fix: non-SSE responses now stream through a bounded reader
without pre-EOF spooling, and SSE event-limit coverage includes CR-only and
mixed CR/LF delimiters split across reads.
Upstream redirect review fix: the transport client returns upstream 3xx
responses unchanged and never follows their `Location`; live behavior tests
cover 301, 302, 307, and 308 redirects to same-origin and cross-origin targets.

T155 added a minimal non-root Docker image with static gateway/gwctl binaries,
readiness healthcheck, and persistent `/data` defaults. T152 hardened semantic
admin IDs/cursors, upstream URL startup validation, structured error logging,
and early request-line limits; T154 audited control-plane secret redaction and
hardened credential-bearing error/header surfaces. T156-T160 completed Compose,
startup validation, version metadata, integration coverage, documentation, and
release-candidate preparation. The Go verification suite and build are green.
The Docker daemon is unavailable, and image build/runtime, gateway and Compose
health, Prometheus scraping, image-size measurement, and vulnerability scanning
were not run and are not claimed as passed. The Compose config check could not
run because this environment also lacks the `docker compose` plugin. The three
stale T157 config assertions are fixed; the T143 request-pagination review fix
binds cursors to normalized filters and reports retained bookmark deletion as
`cursor_expired` with restart guidance. No Web UI, provider routing, retries,
Redis, or PostgreSQL.

Post-release transport validation: explicit `stream:false` chat requests now
buffer bounded upstream SSE into one JSON response, while `stream:true` remains
transparent. Active upstream requests use a single one-hour safety deadline;
there is no shorter response-header or streaming-idle timeout, and client
cancellation still immediately cancels upstream. Docker live tests against
9router covered GPT Luna, Claude Sonnet 5, and Antigravity Gemini 3.8 in both
streaming modes, including long-form responses.

T180 completed the Web UI release integration and final-review fixes: Docker and CI use
verified immutable digests with readable Node.js 22.14.0/npm 10.9.2 inputs, BuildKit
caches npm downloads, production source maps are disabled and checked, and only
deterministic static assets are copied into the non-root distroless image. CI now
builds and smoke-tests the image and installs/runs WebKit with dependencies. The
budget gate resolves every lazy route through the Vite manifest; the command palette
traps focus; the System grid reflows at 320px; secret-handoff browser artifacts are
disabled and cleared; and the RSS test exercises overview, timeseries, and breakdown
cache entries deterministically. Login guidance and the four checked-in screenshots
use ADMIN_CREDENTIAL. README and deployment documentation cover `/ui/`, the
end-to-end operator quickstart, session and HTTPS behavior, browser/accessibility
support, cache troubleshooting, and the UI API surfaces. This local environment
still cannot run Docker because the daemon socket is inaccessible, and WebKit local
execution remains host-library blocked; CI is the supported gate for both.

Web UI direction: React/TypeScript/Vite in `web/`, built into and served by the
Go binary under `/ui/`. The visual target is a dense, responsive dark-first
operations console with a complete light theme, based on screenshots kept in
`docs/ui/reference/`. It must not reproduce unsupported 9router provider/routing
screens. Browser authentication uses short-lived HttpOnly server sessions; the
admin credential must never be persisted in frontend-accessible storage.
Resource budgets are part of acceptance: strict lazy-loaded bundle limits,
bounded pages/charts/DOM/cache, no heavy visual effects or remote fonts, polling
only for visible Overview/System pages at 30s/60s minimum intervals, and at most
two concurrent bounded server aggregation queries. UI work must not regress the
proxy transport hot path.
Usage analytics includes `1h`, `Today`, `24h`, `7d`, `30d`, `90d`, `1y`, custom,
and `All retained` ranges. Long ranges use automatically coarser bounded buckets;
`All retained` explicitly reflects SQLite history still present after retention.
Its product direction is "Bifrost-lite": KPI cards, one tabbed primary chart for
requests/tokens/cost/latency, and one tabbed ranking card for models/keys/outcomes.
No realtime stream, exports, provider catalog, dimension sprawl, or multiple
heavy charts mounted at once.
Frontend architecture is feature-first: app composition, narrowly shared UI/
transport primitives, and isolated Overview, Usage, Keys, Requests, System, and
Auth modules with lazy public route entries. Cross-feature internal imports and
monolithic global API/types/component files are prohibited so each task can be
implemented with a small local context.
