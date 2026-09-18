import React from "react";
import { Link, useLocation } from "react-router-dom";
import { Sun, Moon, ChevronRight, User, LogOut } from "lucide-react";
import { IconButton, StatusPill } from "../../shared/ui";
import { useTheme } from "../../shared/theme";
import { useAuth } from "../../features/auth";

const ROUTE_DESCRIPTIONS: Record<string, { title: string; subtitle: string }> = {
  "/": {
    title: "Overview",
    subtitle: "Gateway throughput, token consumption, and operations status",
  },
  "/overview": {
    title: "Overview",
    subtitle: "Gateway throughput, token consumption, and operations status",
  },
  "/usage": {
    title: "Usage & Analytics",
    subtitle: "Monitor token consumption, request volume, and billing estimates",
  },
  "/keys": {
    title: "API Keys",
    subtitle: "Manage access tokens, key status, and authorization policies",
  },
  "/api-keys": {
    title: "API Keys",
    subtitle: "Manage access tokens, key status, and authorization policies",
  },
  "/requests": {
    title: "Requests",
    subtitle: "Inspect recent gateway requests, response codes, and latency",
  },
  "/system": {
    title: "System Diagnostics",
    subtitle: "Runtime health, SQLite persistence, upstream connectivity, and build info",
  },
  "/login": {
    title: "Sign In",
    subtitle: "Authenticate with your admin credential to access the console",
  },
  "/components": {
    title: "Component Catalog",
    subtitle: "Development-only design system primitives visualizer",
  },
};

export const PageHeader: React.FC = () => {
  const location = useLocation();
  const { theme, toggleTheme } = useTheme();
  const { isAuthenticated, logout } = useAuth();

  const currentPath = location.pathname.replace(/\/$/, "") || "/";
  const defaultNotFound = {
    title: "Not Found",
    subtitle: "The requested route does not exist below /ui/",
  };
  const keysFallback = ROUTE_DESCRIPTIONS["/keys"] ?? defaultNotFound;
  const info =
    ROUTE_DESCRIPTIONS[currentPath] ??
    (currentPath.startsWith("/keys/") ? keysFallback : defaultNotFound);

  return (
    <header className="gw-page-header" data-testid="page-header">
      <div className="gw-page-header-left">
        {/* Breadcrumbs */}
        <nav aria-label="Breadcrumb" className="gw-breadcrumbs">
          <ol className="gw-breadcrumbs-list">
            <li className="gw-breadcrumbs-item">
              <Link to="/overview">Console</Link>
            </li>
            <li aria-hidden="true" className="gw-breadcrumbs-sep">
              <ChevronRight size={12} />
            </li>
            <li className="gw-breadcrumbs-item gw-breadcrumbs-current" aria-current="page">
              {info.title}
            </li>
          </ol>
        </nav>

        {/* Page Title & Subtitle */}
        <div className="gw-page-title-row">
          <h1 className="gw-page-title">{info.title}</h1>
        </div>
        <p className="gw-page-subtitle">{info.subtitle}</p>
      </div>

      {/* Header Right Actions */}
      <div className="gw-page-header-actions">
        <StatusPill label="Gateway Online" />

        <IconButton
          icon={theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}
          aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
          variant="ghost"
          size="md"
          onClick={toggleTheme}
          data-testid="theme-toggle-header"
        />

        {isAuthenticated ? (
          <IconButton
            icon={<LogOut size={18} />}
            aria-label="Sign out"
            title="Sign out of console"
            variant="secondary"
            size="md"
            onClick={() => void logout()}
            data-testid="logout-btn"
          />
        ) : (
          <Link to="/login" title="Operator Sign In" style={{ textDecoration: "none" }}>
            <IconButton
              icon={<User size={18} />}
              aria-label="Operator sign in"
              variant="secondary"
              size="md"
              data-testid="login-link-btn"
            />
          </Link>
        )}
      </div>
    </header>
  );
};
