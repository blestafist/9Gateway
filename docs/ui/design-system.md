# 9Gateway Console Visual System & Design Specifications

Canonical source of truth for visual tokens, UI primitives, typography, density, elevation, iconography, motion, and accessibility across the 9Gateway console.

---

## 1. Design Direction & Reference Decisions

### Primary Direction
The 9Gateway console is a dense, high-contrast, technical operations interface designed for AI proxy operators. The primary visual direction is a **dark charcoal/slate dashboard** with crisp borders, a subtle background grid texture, and a distinctive warm-coral brand accent.

A fully intentional **light theme** is provided alongside dark mode. The light theme is not a mechanical color inversion; it is engineered with crisp light slate surfaces, balanced contrast, and identical functional hierarchies.

### Concrete Decisions Derived from Reference Screenshots (`.references/UI/1.png` & `2.png`)
Visual inspection of the provided reference screenshots informed several key foundations:

1. **Charcoal / Slate Surface Palette** (`1.png` & `2.png`):
   - Background canvas: Deep charcoal (`#0d0f12`), avoiding harsh pure `#000000`.
   - CSS-only subtle grid texture: 32px repeating line grid (`rgba(255, 255, 255, 0.025)`), creating depth without heavy image assets or backdrop blur filters.
   - Surface elevation layers: `#15171e` (Card / Section surface), `#1c1e27` (Raised surface / popovers), and `#252834` (Hover / active).
   - Borders: Precise 1px borders (`#262936`) defining boundaries sharply against dark surfaces.

2. **Telemetry & Metric Color Roles** (`1.png`):
   - Visual inspection of the KPI cards revealed a deliberate role-based color scheme:
     - **Total Requests**: Crisp Slate / White (`#f8fafc` dark / `#0f172a` light) for neutral volume.
     - **Input Tokens**: Warm Coral (`#f06a4b`) serving as both brand highlight and primary ingestion volume.
     - **Cached Tokens**: Vivid Blue (`#3b82f6` dark / `#2563eb` light) for savings and efficiency.
     - **Output Tokens**: Emerald Green (`#10b981` dark / `#059669` light) for successful generation volume.
     - **Estimated Cost**: Warm Amber (`#f59e0b` dark / `#d97706` light) with monospace tabular formatting.

3. **Restrained Warm-Coral Brand Accent** (`1.png` & `2.png`):
   - Used for primary CTA buttons (e.g., "+ Create Key"), active toggle states, metric emphasis, and active route markers.
   - Primary: `#f06a4b` (`hsl(11, 85%, 62%)`).
   - Hover / Active: `#e05638` / `#c84529`.
   - Subtle tinted backgrounds: `rgba(240, 106, 75, 0.12)`.

4. **Compact Density & Segmented Controls** (`1.png` & `2.png`):
   - Dense operations layout with tight padding (12px - 16px cards, 36px - 40px table rows).
   - Segmented pill tabs for view switching (`Overview` / `Details`, time ranges `Today`, `24h`, `7d`, and metric views `Tokens` / `Cost`).
   - Monospace tabular figures for request IDs, token counts, timestamps, and masked credentials (`sk-c47••••••••2494`).
   - Inline action triggers: Reveal eye icon, copy button, and toggle switch grouped in dense key rows.

5. **Exclusions & Boundaries**:
   - 9router-specific multi-provider routing graphs, proxy pools, combo adapters, and skills shown in the references are strictly excluded as out of 9Gateway's proxy scope.
   - No runtime external fonts or CDNs; font stacks rely solely on local system fonts.
   - No broad blur filters (`backdrop-filter: blur(...)`) on scroll surfaces to protect transport rendering performance and low-end CPU devices.

---

## 2. Color System & Semantic Tokens

All UI components consume CSS custom properties defined at `:root` (light) and `[data-theme="dark"]` (default). Raw hex or RGB colors are prohibited inside feature components.

### 2.1 CSS Custom Properties Map

```css
/* Dark Theme (Default) */
[data-theme="dark"] {
  /* Surfaces & Canvas */
  --bg-canvas: #0d0f12;
  --bg-grid-line: rgba(255, 255, 255, 0.028);
  --bg-surface-1: #15171e;       /* Cards, sidebar */
  --bg-surface-2: #1c1e27;       /* Raised panels, inputs, popovers */
  --bg-surface-3: #252834;       /* Hover states, active segmented items */
  --bg-overlay: rgba(0, 0, 0, 0.72);

  /* Borders & Dividers */
  --border-subtle: #20232e;
  --border-default: #2a2d3b;
  --border-strong: #3b3f52;
  --border-focus: #f06a4b;

  /* Typography */
  --text-primary: #f8fafc;       /* Contrast > 14:1 */
  --text-secondary: #94a3b8;     /* Contrast > 5.5:1 */
  --text-muted: #64748b;         /* Contrast > 4.5:1 for standard text */
  --text-on-accent: #ffffff;
  --text-inverse: #0f172a;

  /* Brand / Primary Accent (Warm Coral) */
  --accent-primary: #f06a4b;
  --accent-hover: #fa7b5e;
  --accent-active: #de5839;
  --accent-subtle: rgba(240, 106, 75, 0.14);
  --accent-border: rgba(240, 106, 75, 0.35);

  /* Telemetry & Metrics */
  --metric-requests: #f8fafc;
  --metric-tokens-in: #f06a4b;
  --metric-tokens-cache: #3b82f6;
  --metric-tokens-out: #10b981;
  --metric-cost: #f59e0b;

  /* Semantic Feedback */
  --status-success: #10b981;
  --status-success-subtle: rgba(16, 185, 129, 0.12);
  --status-success-border: rgba(16, 185, 129, 0.32);
  --status-warning: #f59e0b;
  --status-warning-subtle: rgba(245, 158, 11, 0.12);
  --status-warning-border: rgba(245, 158, 11, 0.32);
  --status-danger: #ef4444;
  --status-danger-subtle: rgba(239, 68, 68, 0.14);
  --status-danger-border: rgba(239, 68, 68, 0.35);
  --status-info: #3b82f6;
  --status-info-subtle: rgba(59, 130, 246, 0.12);
  --status-info-border: rgba(59, 130, 246, 0.32);

  /* Shadows */
  --shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.4);
  --shadow-md: 0 4px 12px rgba(0, 0, 0, 0.5);
  --shadow-lg: 0 8px 24px rgba(0, 0, 0, 0.65);
}

/* Light Theme */
[data-theme="light"] {
  /* Surfaces & Canvas */
  --bg-canvas: #f8fafc;
  --bg-grid-line: rgba(15, 23, 42, 0.035);
  --bg-surface-1: #ffffff;       /* Cards */
  --bg-surface-2: #f1f5f9;       /* Raised panels, input backgrounds */
  --bg-surface-3: #e2e8f0;       /* Hover states, segmented items */
  --bg-overlay: rgba(15, 23, 42, 0.5);

  /* Borders & Dividers */
  --border-subtle: #f1f5f9;
  --border-default: #e2e8f0;
  --border-strong: #cbd5e1;
  --border-focus: #dc4f2f;

  /* Typography */
  --text-primary: #0f172a;       /* Contrast > 15:1 */
  --text-secondary: #475569;     /* Contrast > 7:1 */
  --text-muted: #64748b;         /* Contrast > 4.5:1 */
  --text-on-accent: #ffffff;
  --text-inverse: #f8fafc;

  /* Brand / Primary Accent */
  --accent-primary: #e05638;
  --accent-hover: #cc4527;
  --accent-active: #b5381d;
  --accent-subtle: rgba(224, 86, 56, 0.08);
  --accent-border: rgba(224, 86, 56, 0.28);

  /* Telemetry & Metrics */
  --metric-requests: #0f172a;
  --metric-tokens-in: #d9492b;
  --metric-tokens-cache: #2563eb;
  --metric-tokens-out: #059669;
  --metric-cost: #d97706;

  /* Semantic Feedback */
  --status-success: #059669;
  --status-success-subtle: rgba(5, 150, 105, 0.08);
  --status-success-border: rgba(5, 150, 105, 0.28);
  --status-warning: #d97706;
  --status-warning-subtle: rgba(217, 119, 6, 0.08);
  --status-warning-border: rgba(217, 119, 6, 0.28);
  --status-danger: #dc2626;
  --status-danger-subtle: rgba(220, 38, 38, 0.08);
  --status-danger-border: rgba(220, 38, 38, 0.28);
  --status-info: #2563eb;
  --status-info-subtle: rgba(37, 99, 235, 0.08);
  --status-info-border: rgba(37, 99, 235, 0.28);

  /* Shadows */
  --shadow-sm: 0 1px 2px rgba(15, 23, 42, 0.05);
  --shadow-md: 0 4px 12px rgba(15, 23, 42, 0.08);
  --shadow-lg: 0 8px 24px rgba(15, 23, 42, 0.12);
}
```

---

## 3. Typography & Spacing Scale

### 3.1 Font Stacks
System and local font stacks are strictly required:
- **Interface Font**:
  `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`
- **Monospace Font** (telemetry, tokens, API keys, JSON payloads, timestamps):
  `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace`

### 3.2 Type Hierarchy
All typography scales with `rem` units to ensure accessibility at 200% text zoom:

| Token | Size (rem / px) | Weight | Line Height | Purpose |
|---|---|---|---|---|
| `--font-xs` | 0.6875rem (11px) | 500 | 1.4 | Badges, micro labels, table footers |
| `--font-sm` | 0.75rem (12px) | 400 / 500 | 1.4 | Table cells, helper text, timestamps |
| `--font-base` | 0.875rem (14px) | 400 / 500 | 1.5 | Body text, inputs, buttons, table headers |
| `--font-md` | 1.0rem (16px) | 500 / 600 | 1.5 | Subheadings, card titles, large buttons |
| `--font-lg` | 1.125rem (18px) | 600 | 1.4 | Modal titles, section headers |
| `--font-xl` | 1.5rem (24px) | 600 / 700 | 1.3 | Page titles |
| `--font-metric`| 1.75rem - 2.0rem | 700 | 1.2 | Big telemetry KPI numbers (tabular figures) |

### 3.3 Spacing Rhythm (8px Base System)

```css
--space-1: 0.25rem;  /* 4px  - micro gaps, badge padding */
--space-2: 0.5rem;   /* 8px  - element gaps, dense row padding */
--space-3: 0.75rem;  /* 12px - input padding, card inner padding */
--space-4: 1.0rem;   /* 16px - standard card padding, component margins */
--space-5: 1.25rem;  /* 20px - section spacing */
--space-6: 1.5rem;   /* 24px - page padding, modal layout */
--space-8: 2.0rem;   /* 32px - major section dividers */
--space-12: 3.0rem;  /* 48px - empty states, spacious layouts */
```

### 3.4 Radii

```css
--radius-sm: 4px;    /* Input fields, small badges, check boxes */
--radius-md: 6px;    /* Buttons, segmented pills, select controls */
--radius-lg: 8px;    /* Cards, modal dialogs, drawers */
--radius-xl: 12px;   /* Elevated panels */
--radius-full: 9999px; /* Status dots, full pill tags, switch track */
```

---

## 4. Iconography Standards

- All icons are rendered via SVG from `lucide-react`.
- Emojis (e.g. 🔑, 🚀, ⚠️) are strictly forbidden as UI icons or structural indicators.
- Sizing tokens:
  - Small: `14px` (inline with 12px text or badges)
  - Regular: `16px` (buttons, inputs, menu items)
  - Medium: `18px` - `20px` (card headers, modal titles)
  - Large: `32px` - `40px` (empty states)
- Stroke width: uniform `1.75px` across all icons for visual cohesion.
- Accessibility:
  - Decorative icons alongside text must have `aria-hidden="true"`.
  - Icon-only buttons (`IconButton`) must supply a mandatory `aria-label`.

---

## 5. Motion, Focus & Accessibility Guidelines

### 5.1 Motion
- Subtle, functional micro-interactions only (hover, active, focus transitions: `150ms ease-out`).
- Overlays (dialogs, drawers, toasts): `200ms cubic-bezier(0.16, 1, 0.3, 1)`.
- No GSAP or complex parallax/canvas animations.
- Respecting user preferences:
  ```css
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after {
      animation-duration: 0.001ms !important;
      animation-iteration-count: 1 !important;
      transition-duration: 0.001ms !important;
      scroll-behavior: auto !important;
    }
  }
  ```

### 5.2 Focus Rings
- Focused elements must always display a high-contrast focus ring:
  ```css
  :focus-visible {
    outline: 2px solid var(--border-focus);
    outline-offset: 2px;
  }
  ```
- Focus is never removed via `outline: none` without a `:focus-visible` replacement.

### 5.3 Touch Targets & Contrast
- Minimum touch target area is `44px x 44px` on interactive controls where space permits.
- All body and informative text meets WCAG AA contrast (≥ 4.5:1).
- Non-text controls and active borders meet WCAG AA contrast (≥ 3:1).
- High Contrast Mode (`prefers-contrast: more`) thickens borders and removes translucent backgrounds.

---

## 6. Shared UI Primitives Specification

The following atomic, composable components form the core design primitive library in `src/shared/ui/`:

1. **Button**: Primary (warm coral), Secondary, Outline, Ghost, Danger variants. Supports `sm`, `md`, `lg`, loading spinner, disabled state.
2. **IconButton**: Icon-only button with guaranteed `aria-label`, standard 44px hit-box envelope, tooltips.
3. **Input**: Accessible text field with label, error text, helper text, and optional prefix/suffix adornments (e.g. copy, eye reveal).
4. **Select**: Custom styled native `<select>` with chevron indicator and full keyboard accessibility.
5. **Checkbox**: Accessible checkbox with checkmark SVG, keyboard activation, error state.
6. **Switch**: Accessible toggle switch (`role="switch"`, `aria-checked`), coral active state as seen in reference `2.png`.
7. **Badge**: Status pill (`default`, `success`, `warning`, `danger`, `info`, `neutral`) with optional pulse/dot indicator.
8. **Card**: Surface container (`CardHeader`, `CardTitle`, `CardDescription`, `CardContent`, `CardFooter`).
9. **Tabs**: Segmented pill-style or line-style tabs with ARIA tablist/tab/tabpanel keyboard navigation (Left/Right arrows).
10. **Tooltip**: Accessible hover and focus tooltip (`role="tooltip"`), dismissed on `Escape`.
11. **Dialog / Drawer**: Modal dialog and sliding drawer with accessible focus trapping, focus restoration on close, `Escape` key dismiss, and backdrop dismissal.
12. **Table Shell**: Dense data table wrapper (`Table`, `TableHeader`, `TableBody`, `TableRow`, `TableHead`, `TableCell`), sticky headers, sort indicators.
13. **Skeleton**: Content placeholder with pulse animation (respecting reduced motion).
14. **EmptyState**: Standard container for empty datasets with icon, title, description, and action button.
15. **Alert**: Semantic status banner (`info`, `warning`, `danger`, `success`) with matching Lucide icons.
16. **Toast Region**: Accessible status messaging using `aria-live="polite"` (info/success) and `aria-live="assertive"` (danger). Auto-dismissing with manual close.
