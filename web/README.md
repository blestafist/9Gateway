# 9Gateway Web UI

Embedded React, TypeScript, and Vite single-page console for 9Gateway.

Node.js is build-time only. Production serves static embedded assets from the compiled Go gateway binary under `/ui/`.

## Directory Structure and Placement Rules

To keep agent context bounded, this frontend follows a strict feature-first architecture enforced by linter boundary rules:

- `src/app/`: Top-level composition, router configuration, application-wide providers, and shell layout.
  - Allowed dependencies: public feature entry points (`src/features/<feature>/index.ts`) and shared primitives (`src/shared/**`).
- `src/features/<feature>/`: Route-owned modules (e.g., `overview`, `usage`, `keys`, `requests`, `system`, `auth`).
  - Contains route components, local UI, hooks, queries, feature types, and feature tests.
  - Public contract is exposed strictly via `index.ts`.
  - Import boundary rule: features **cannot** reach into another feature's internal directories.
  - Allowed dependencies: own feature files, `src/shared/**`, and public entry points of other features.
- `src/shared/`: Proven, generic UI and utility primitives.
  - Contains reusable design system components and domain-agnostic helpers.
  - Import boundary rule: shared code **cannot** import application composition (`src/app/**`) or features (`src/features/**`).
- `src/fixtures/`: Lint and component fixture files used for testing boundaries or component permutations.
  - Boundary fixtures live in `src/fixtures/boundaries/`.
- `src/tests/` (and co-located `*.test.tsx` / `*.test.ts`):
  - Unit and component tests are co-located with their respective features or shared primitives.
  - Cross-cutting architectural and boundary tests reside in `src/tests/`.

## Development Commands

Run the Go gateway backend and Vite development server together:

1. **Start the Go Gateway**:
   ```bash
   go run ./cmd/gateway -config config.yaml
   ```
   Listens on `http://127.0.0.1:8080`.

2. **Start the Vite Dev Server**:
   ```bash
   npm --prefix web run dev
   ```
   Opens on `http://localhost:5173/ui/`.
   Vite's dev proxy forwards `/v1`, `/admin`, `/health`, `/ready`, and `/metrics` requests to `http://127.0.0.1:8080` for same-origin API access.

## Production Build and Verification

- **Lint**: `npm --prefix web run lint`
- **Test**: `npm --prefix web run test`
- **Build**: `npm --prefix web run build`

The build outputs to `web/dist/`, which is embedded into the Go gateway via Go's `embed.FS` (`github.com/pestit/9gateway/web`). In production, the gateway serves `/ui/` directly without Node.js.
