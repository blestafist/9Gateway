import React, { lazy, Suspense } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { AppShell } from "./shell/AppShell";
import { RouteErrorBoundary } from "./shell/RouteErrorBoundary";
import { RouteLoadingFallback } from "./shell/RouteLoadingFallback";

const OverviewPage = lazy(() => import("../features/overview"));
const UsagePage = lazy(() => import("../features/usage"));
const KeysPage = lazy(() => import("../features/keys"));
const RequestsPage = lazy(() => import("../features/requests"));
const SystemPage = lazy(() => import("../features/system"));
const LoginPage = lazy(() => import("../features/auth"));
const NotFoundPage = lazy(() => import("../features/not-found"));

// Lazily load ComponentCatalog only in development
const ComponentCatalog = import.meta.env.DEV
  ? lazy(() => import("../features/component-catalog"))
  : null;

const renderLazyRoute = (Component: React.ComponentType) => (
  <RouteErrorBoundary>
    <Suspense fallback={<RouteLoadingFallback />}>
      <Component />
    </Suspense>
  </RouteErrorBoundary>
);

export const AppRoutes: React.FC = () => {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="overview" element={renderLazyRoute(OverviewPage)} />
        <Route path="usage" element={renderLazyRoute(UsagePage)} />
        <Route path="keys" element={renderLazyRoute(KeysPage)} />
        <Route path="api-keys" element={<Navigate to="/keys" replace />} />
        <Route path="requests" element={renderLazyRoute(RequestsPage)} />
        <Route path="system" element={renderLazyRoute(SystemPage)} />
        <Route path="login" element={renderLazyRoute(LoginPage)} />

        {import.meta.env.DEV && ComponentCatalog && (
          <Route path="components" element={renderLazyRoute(ComponentCatalog)} />
        )}

        <Route path="*" element={renderLazyRoute(NotFoundPage)} />
      </Route>
    </Routes>
  );
};
