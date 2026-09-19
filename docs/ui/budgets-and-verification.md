# 9Gateway Console Budgets, Browser Verification Matrix & Quality Gates

This document defines the automated performance budgets, bundle size limits, architectural dependency boundaries, cross-browser Playwright verification matrix, and continuous integration gates for the 9Gateway Web UI console.

---

## 1. Browser Verification Matrix

End-to-end user journeys, performance checks, and security invariants are executed against a live gateway binary with an active SQLite instance and mock upstream server.

| Project / Platform | Target Device / Emulation | Test Scope | Status | Notes |
| :--- | :--- | :--- | :--- | :--- |
| **Chromium** | Desktop Chrome (Dark mode) | Critical journeys, security, performance | **PASS** (17/17 tests) | Full browser and devtools metrics |
| **Chromium-Light** | Desktop Chrome (Light mode) | Critical operator journeys | **PASS** (8/8 tests) | Validates light theme token contrast & components |
| **Mobile-Chromium** | Google Pixel 5, mobile drawer nav | Critical journeys, performance | **PASS** (12/12 tests) | Validates responsive drawer, card lists, touch targets |
| **Firefox** | Desktop Firefox (Dark mode) | Critical operator journeys | **PASS** (8/8 tests) | Validates Gecko engine rendering and events |
| **WebKit** | Desktop Safari | Critical operator journeys | **BLOCKED** | Host system missing `libicu74`, `libxml2`, `libflite1` |

### WebKit Environment Block Note
CI installs WebKit with `npx playwright install --with-deps webkit` and runs the dedicated `npm run test:e2e:webkit` gate. In local headless Linux environments where system-level WebKit runtime libraries (`libicu74`, `libxml2`, `libflite1`) are not provisioned, WebKit fails at browser launch (`browserType.launch: Host system is missing dependencies to run browsers`); this local gate remains blocked rather than being reported as passed.

---

## 2. Production Bundle & Asset Budgets

The build artifact sizes are enforced by `web/scripts/budget-check.js` and verified by `web/src/tests/budgets.test.ts`:

| Asset / Scope | Budget Metric | Configured Limit | Measured Value |
| :--- | :--- | :--- | :--- |
| **Initial Entry Point** | Uncompressed JS | <= 480 KiB | ~437 KiB |
| **Initial Entry Point** | Gzip JS | <= 150 KiB | ~124 KiB |
| **Total JavaScript** | All bundled chunks | <= 1200 KiB | ~1002 KiB |
| **Total CSS** | All stylesheets combined | <= 100 KiB | ~78 KiB |
| **Overview Chunk** | Uncompressed JS | <= 50 KiB | ~25 KiB |
| **Keys Inventory Chunk** | Uncompressed JS | <= 70 KiB | ~65 KiB |
| **Requests Explorer Chunk** | Uncompressed JS | <= 80 KiB | ~47 KiB |
| **System Diagnostics Chunk**| Uncompressed JS | <= 50 KiB | ~24 KiB |
| **Login Route Chunk** | Uncompressed JS | <= 30 KiB | ~3 KiB |
| **Usage Route Chunk** | Uncompressed JS (incl. Recharts) | <= 440 KiB | ~437 KiB |

---

## 3. Architectural Import Boundaries & Dependency Tracking

Architectural boundaries are enforced by `web/scripts/dependency-report.js` and Vitest:

1. **Zero Cross-Feature Imports**: Feature modules (`features/overview`, `features/usage`, `features/keys`, `features/requests`, `features/system`, `features/auth`) must never import private components or helpers from sibling feature folders.
2. **Shared Module Governance**: Common functionality must reside in `src/shared/*`. Pervasive modules (imported by >= 3 route features) are monitored to prevent architectural bloating:
   - `shared/transport`: Authoritative HTTP client, CSRF headers, and error parsing (used by all 6 routes).
   - `shared/formatters`: Shared dates, currencies, and byte formatters (used by 5 routes).
   - `shared/ui`: Reusable design system primitives (used by 4 routes).

---

## 4. End-to-End Security & Performance Invariants

### Security Controls
- **Security Headers**: CSP (`default-src 'self'`, `script-src 'self'`, `frame-ancestors 'none'`), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`.
- **Session Protection**: `gw_session` cookie is issued as `HttpOnly`, `SameSite=Strict`, completely invisible to client JavaScript (`document.cookie`).
- **CSRF Token Validation**: Cookie-authenticated state mutations (`POST`, `PUT`, `DELETE`) without valid `X-CSRF-Token` headers are strictly rejected with HTTP 403.
- **Safe Payload Rendering**: Safe body viewer sanitizes and renders payload bodies in plaintext and hexadecimal modes; script execution or HTML tag injection is neutralized.
- **Credential Scrubbing**: Session revocation clears memory state, TanStack query caches, and local storage tokens, preventing restoration via browser back navigation.

### Runtime Performance Invariants
- **DOM Node Count**: Primary console routes maintain bounded DOM size (< 2500 elements).
- **Cumulative Layout Shift (CLS)**: Navigations between primary routes maintain CLS < 0.1.
- **Idle Network Silence**: Routes without explicit background polling generate 0 network requests during idle periods.
- **Input Latency**: Primary route navigation and UI interactions generate zero long tasks exceeding 200 ms.

### Gateway Hot Path Invariants
- **Zero Proxy Buffering**: Embedded UI asset delivery does not intercept, buffer, or alter proxy first-byte delivery (TTFB) or per-chunk streaming lifetimes (`TestT179_ProxyHotPathInvariantsWithUI`).
- **Bounded Resident Memory**: Serving UI assets and 32 cached analytics query windows keeps gateway RSS growth under 32 MiB (`TestT179_GatewayRSSGrowthUnderUIAndCachedAnalytics`).

---

## 5. Verification Commands & CI Gates

```bash
# Frontend quality and unit tests
npm --prefix web run lint
npm --prefix web run test
npm --prefix web run build

# Production bundle budget and architecture checks
npm --prefix web run budget:check
npm --prefix web run report:dependencies

# End-to-end browser suite (Chromium, Chromium-Light, Mobile-Chromium, Firefox)
npm --prefix web run test:e2e

# Go backend hot path and RSS growth tests
go test -v ./internal/httpserver -run TestT179
go test ./...
go build ./...
```
