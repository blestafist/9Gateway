# Current Work

Current milestone: embedded Web UI (`T161`-`T180`).

Done: `T001`-`T164`.

Current: `T165` - implement the typed admin data layer.

Queued: `T166`-`T180`, in dependency order from admin overview aggregation API
through analytics, key/request workflows, quality gates, and release integration.

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
