import React, { lazy, Suspense } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { AppShell } from "./shell/AppShell";
import { RouteErrorBoundary } from "./shell/RouteErrorBoundary";
import { RouteLoadingFallback } from "./shell/RouteLoadingFallback";
import { ProtectedRoute } from "./ProtectedRoute";

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

const renderProtectedLazyRoute = (Component: React.ComponentType) => (
  <ProtectedRoute>
    {renderLazyRoute(Component)}
  </ProtectedRoute>
);

export const AppRoutes: React.FC = () => {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="overview" element={renderProtectedLazyRoute(OverviewPage)} />
        <Route path="usage" element={renderProtectedLazyRoute(UsagePage)} />
        <Route path="keys" element={renderProtectedLazyRoute(KeysPage)} />
        <Route path="api-keys" element={<Navigate to="/keys" replace />} />
        <Route path="requests" element={renderProtectedLazyRoute(RequestsPage)} />
        <Route path="system" element={renderProtectedLazyRoute(SystemPage)} />
        <Route path="login" element={renderLazyRoute(LoginPage)} />

        {import.meta.env.DEV && ComponentCatalog && (
          <Route path="components" element={renderLazyRoute(ComponentCatalog)} />
        )}

        <Route path="*" element={renderLazyRoute(NotFoundPage)} />
      </Route>
    </Routes>
  );
};
