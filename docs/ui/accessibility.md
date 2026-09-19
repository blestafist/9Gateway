# 9Gateway Console Accessibility & Responsive Verification Matrix

This document defines the accessibility and responsive testing matrix, WCAG 2.2 Level AA compliance verification, automated scanner coverage, manual audit protocols, and repeatable commands for the 9Gateway Web UI console.

---

## 1. Compliance Standard & Scope

- **Target Standard**: WCAG 2.2 Level AA (Web Content Accessibility Guidelines 2.2 AA).
- **Core Principles**: Perceivable, Operable, Understandable, Robust (POUR).
- **Audit Scope**:
  - Sign In / Authentication (`/ui/login`)
  - App Shell, Header, Sidebar, Mobile Drawer, Breadcrumbs, and Notifications Region
  - Command Palette (`Ctrl+K` / `⌘K`)
  - System Overview (`/ui/overview`)
  - Usage & Analytics (`/ui/usage`), including Recharts chart cards and alternative data tables
  - API Keys Inventory & Filtering (`/ui/keys`)
  - API Key Creation Modal & One-Time Secret Handoff
  - API Key Policy Editor Drawer & High-Impact Confirmation Modal
  - Request History Explorer & Pagination (`/ui/requests`)
  - Request Detail View & Timeline Waterfall (`/ui/requests/:id`)
  - Safe Body Viewer (Text and Hex views, byte counter, truncation notices)
  - System Diagnostics Dashboard (`/ui/system`) & Safe Snapshot Modal
  - Route Error Boundaries & Loading Skeletons
  - Composable UI Primitives (`web/src/shared/ui`)

---

## 2. Automated Scanner Gate (`axe-core`)

Automated accessibility audits are integrated directly into Vitest via `axe-core` in `web/src/tests/a11y.test.tsx`.

### Acceptance Criteria
- **Zero Serious or Critical Violations**: The automated test suite enforces `expect(seriousOrCritical).toEqual([])` across all audited screens and modals.
- **No Undocumented Rule Ignores**: All rules evaluated by axe run against WCAG 2.0, 2.1, and 2.2 tags (`wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`, `wcag22aa`).
- **Clean ARIA Controls**: Dynamic tabs, accordions, and panels conditionally bind `aria-controls` only when target DOM elements are present to avoid broken ARIA reference pointers.

### Exact Automated Commands
```bash
# Run complete axe-core automated audit suite
npm --prefix web run test -- a11y

# Run complete web test suite
npm --prefix web run test

# Run linter and type-checker
npm --prefix web run lint
npm --prefix web run build
```

---

## 3. Screen-by-Screen Test Matrix

| Screen / Component | Automated Axe Result | Landmarks & Headings | Keyboard Navigation & Focus Trap | Live Regions & Feedback | Responsive Behavior (320px–1440px) |
|---|---|---|---|---|---|
| **Login (`/login`)** | Pass (0 violations) | `<main>`, `<h1>` page heading | Visible focus outline, Tab order: input -> submit button | Form validation error announced | Fluid card, full-width button at <= 375px |
| **Shell & Nav** | Pass (0 violations) | `<header>`, `<nav aria-label="Main Navigation">`, `<aside>`, `<main id="main-content">` | Skip link to `#main-content`, `aria-current="page"`, Escape closes mobile drawer | Toast announcements via polite live region | Collapsible desktop sidebar (240px -> 64px), mobile hamburger drawer below 768px |
| **Command Palette** | Pass (0 violations) | `role="dialog"`, `aria-modal="true"` | Focus locked to dialog, arrow down/up selection, Enter to execute, Escape to dismiss | Result count polite announcement | Max-width 640px, full-width inset with padding on mobile |
| **Overview** | Pass (0 violations) | `<h1>`, `<section>`, `<h3>` card titles | Tab stops for period presets and refresh button | Background refresh notice and stale data warnings | 4-column KPI grid reflows to 2 cols (768px) and 1 col (375px) |
| **Usage & Charts** | Pass (0 violations) | Tabbed chart container, `<table aria-label="...">` | Tab stops for presets, metric tabs, and table view toggle | Screen-reader text summaries for chart trends | Chart height bounded; data table inside scrollable region with keyboard reachability |
| **API Keys Inventory** | Pass (0 violations) | Search filter controls, pagination navigation | Deep-link table rows and action buttons keyboard actionable | Filter match count announced via `role="status"` | Desktop table switches to responsive mobile cards on narrow viewports |
| **Create Key Dialog** | Pass (0 violations) | `role="dialog"`, `aria-modal="true"`, heading `<h2>` | Focus trap within dialog, auto-focus on name input | Copy-to-clipboard confirmation polite announcement | Dialog size `md` adapts to 100% width with 16px margins on <=375px |
| **Key Policy Drawer** | Pass (0 violations) | `role="dialog"` drawer shell, clear section titles | Focus trapped in drawer while open, restored to triggering row on close | Dirty edit alert on close attempt | Drawer slides from right on desktop; full-width bottom sheet on mobile |
| **Requests Explorer** | Pass (0 violations) | Filter toolbar, table shell | Table row Enter/Space deep navigation, copy ID action button | Copy ID announced polite live region | Desktop table with sticky headers; mobile cards on viewports < 768px |
| **Request Details** | Pass (0 violations) | Breadcrumb parent navigation, status badge | Accessible back navigation, waterfall timeline labeled | Error toast on body download failure | Multi-column meta grid reflows to single column on mobile |
| **Body Viewer** | Pass (0 violations) | `role="region"` for code previews, tabpanel semantics | Keyboard reachable text/hex scroll regions (`tabIndex={0}`) | Truncation and byte count warning banners | Monospace pre wrap preserves boundaries without window overflow |
| **System Diagnostics** | Pass (0 violations) | Readiness check list, metric cards | Tab navigation through check cards, modal trigger | Copy-to-clipboard toast confirmation | Grid reflows from 3 cols to 1 col cleanly |
| **Error Boundary** | Pass (0 violations) | `role="alert"`, `title="Failed to Load Route"` | "Return to Overview" and "Retry" buttons accessible | Error message conveyed via semantic alert | Centered card responsive across all widths |

---

## 4. Responsive Design & Reflow Verification

### Viewport Gate
The operations console layout is verified across five standard viewport widths:
1. **320px (iPhone SE narrow portrait / 400% zoom at 1280px)**:
   - Root container applies `overflow-x: hidden` preventing accidental document horizontal scrollbars.
   - Touch targets meet or exceed minimum 44×44px interactive tap area.
   - Primary KPI grids and stat cards stack vertically into a single column.
   - Mobile header displays brand and navigation trigger.
2. **375px (Standard mobile device portrait)**:
   - Tables automatically render semantic card list views (`gw-keys-cards-view`, `gw-requests-cards-view`).
   - Action controls reflow to full-width or segmented wraps.
3. **768px (Tablet portrait / iPad)**:
   - 2-column dashboard layout.
   - Sidebar collapses to icon-only navigation with tooltips.
4. **1024px (Tablet landscape / small laptop)**:
   - Full desktop layout active with expanded sidebar (240px).
   - Desktop data tables rendered with column headers and tabular numbers.
5. **1440px (Standard desktop workstation)**:
   - Max-width content boundaries maintain readable line lengths.

### Zoom & Reflow (WCAG 2.2 SC 1.4.4 & SC 1.4.10)
- **200% Text Zoom**: All typography uses `rem` units based on browser root `font-size: 100%`. Text scales up to 200% without clipping, truncation, or overlapping content.
- **400% Reflow**: Content reflows into a single column without requiring two-dimensional scrolling. Intentional data regions (such as wide telemetry tables and hex dumps) contain horizontal scroll within their labeled region (`gw-table-container`, `role="region"`, `tabIndex={0}`).

### Safe Areas
- Root shell container and mobile headers consume `env(safe-area-inset-top)`, `env(safe-area-inset-right)`, `env(safe-area-inset-bottom)`, and `env(safe-area-inset-left)` to prevent notch, camera cutout, and gesture indicator collisions on mobile operating systems.

---

## 5. Visual, Contrast, and Sensory Adaptations

### Contrast Standards (WCAG 2.2 SC 1.4.3 & SC 1.4.11)
- **Primary Text Contrast**: Slate/White on canvas exceeds 14:1 in dark mode and 12:1 in light mode (WCAG AA requirement: 4.5:1).
- **Secondary / Muted Text**: Contrast ratio maintained above 5.5:1.
- **UI Components & Borders**: Interactive boundaries and focus rings (`--border-focus: #f06a4b`) exceed 3:1 against adjacent background surfaces.
- **No Color-Only Information**: All telemetry statuses, readiness checks, outcome badges, and validation messages pair color with explicit icons, badges, and text labels (e.g. Check icon for success, AlertTriangle for failure).

### Forced Colors Mode (Windows High Contrast)
- Dedicated `@media (forced-colors: active)` overrides ensure:
  - Focused interactive elements display `3px solid Highlight` outlines.
  - Buttons, inputs, dialogs, and cards retain `1px solid ButtonBorder` boundaries.
  - Primary call-to-actions adapt to `Highlight` background and `HighlightText`.

### Reduced Motion (WCAG 2.2 SC 2.3.3)
- Dedicated `@media (prefers-reduced-motion: reduce)` overrides:
  - Collapse transition and animation durations to `0.001ms`.
  - Disable smooth scrolling in favor of immediate jump navigation (`scroll-behavior: auto`).

---

## 6. Manual Keyboard Testing Checklist

Execute these manual steps in a browser session to verify keyboard operation:
1. **Tab Traversal**:
   - Press `Tab` upon page load; verify the "Skip to main content" link appears first and focusing it scrolls to and focuses `#main-content`.
   - Verify every link, button, input, select, and checkbox has a visible coral focus outline (`--border-focus`).
2. **Modal Dialogs & Drawers**:
   - Open Create Key dialog: verify focus moves to the first input ("Key Name"); verify `Tab` cycles within the dialog; verify `Escape` closes the dialog and restores focus to the trigger button.
   - Open Key Detail drawer: verify focus moves inside the drawer; verify `Escape` closes the drawer and restores focus to the key's table row.
3. **Command Palette**:
   - Press `Ctrl+K` (or `⌘K` on macOS): verify palette opens immediately with focus in the search input.
   - Use `ArrowDown` and `ArrowUp` to navigate command items; press `Enter` to activate navigation or theme change.
4. **Scrollable Regions**:
   - Navigate to `/ui/requests`: press `Tab` until the table scroll region is focused. Use `ArrowLeft` and `ArrowRight` keys to scroll horizontally without document-level window scroll.
