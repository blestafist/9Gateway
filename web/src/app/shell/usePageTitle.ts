import { useEffect } from "react";
import { useLocation } from "react-router-dom";

const ROUTE_TITLES: Record<string, string> = {
  "/": "Overview",
  "/overview": "Overview",
  "/usage": "Usage & Analytics",
  "/keys": "API Keys",
  "/api-keys": "API Keys",
  "/requests": "Requests",
  "/system": "System Diagnostics",
  "/login": "Sign In",
  "/components": "Component Catalog",
};

export function getRouteTitle(pathname: string): string {
  // Exact match first
  if (ROUTE_TITLES[pathname]) {
    return ROUTE_TITLES[pathname];
  }
  // Base path match if query or nested
  const normalized = pathname.replace(/\/$/, "");
  if (ROUTE_TITLES[normalized]) {
    return ROUTE_TITLES[normalized];
  }
  if (normalized.startsWith("/keys/")) {
    return "API Keys";
  }
  return "Page Not Found";
}

export function usePageTitle(): string {
  const location = useLocation();
  const title = getRouteTitle(location.pathname);

  useEffect(() => {
    document.title = `${title} | 9Gateway`;
  }, [title]);

  return title;
}
