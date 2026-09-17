import React, { Suspense, lazy } from "react";
import { SmokeStatus } from "../features/smoke";

// Lazily load ComponentCatalog only in development
const ComponentCatalog = import.meta.env.DEV
  ? lazy(() => import("../features/component-catalog"))
  : null;

export const App: React.FC = () => {
  const isDev = Boolean(import.meta.env.DEV);
  const pathname = typeof window !== "undefined" ? window.location.pathname : "";
  const hash = typeof window !== "undefined" ? window.location.hash : "";
  const search = typeof window !== "undefined" ? window.location.search : "";

  const isCatalogRoute =
    isDev &&
    (pathname === "/ui/components" ||
      pathname.endsWith("/ui/components") ||
      hash === "#/ui/components" ||
      search.includes("view=components"));

  if (isCatalogRoute && ComponentCatalog) {
    return (
      <Suspense
        fallback={
          <div style={{ padding: "2rem", color: "var(--text-secondary)" }}>
            Loading component catalog...
          </div>
        }
      >
        <ComponentCatalog />
      </Suspense>
    );
  }

  return (
    <main
      style={{
        fontFamily: "var(--font-sans, system-ui, sans-serif)",
        padding: "2rem",
        maxWidth: "600px",
        margin: "0 auto",
      }}
    >
      <h1>9Gateway</h1>
      <p>Embedded Web UI console smoke page.</p>
      <SmokeStatus status="operational" />
      {isDev && (
        <div style={{ marginTop: "1.5rem" }}>
          <a
            href="/ui/components"
            style={{
              color: "var(--accent-primary, #f06a4b)",
              textDecoration: "underline",
              fontSize: "0.875rem",
            }}
          >
            Open Development Component Catalog (/ui/components)
          </a>
        </div>
      )}
    </main>
  );
};

export default App;
