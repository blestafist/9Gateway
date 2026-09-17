import React, { useState, useEffect } from "react";
import { Outlet, NavLink, Link, useLocation } from "react-router-dom";
import { Menu, Zap, Sun, Moon } from "lucide-react";
import { Drawer, IconButton, StatusPill } from "../../shared/ui";
import { useTheme } from "../../shared/theme";
import { SkipLink } from "./SkipLink";
import { Sidebar, NAV_ITEMS } from "./Sidebar";
import { PageHeader } from "./PageHeader";
import { RouteErrorBoundary } from "./RouteErrorBoundary";
import { RouteLoadingFallback } from "./RouteLoadingFallback";
import { usePageTitle } from "./usePageTitle";
import "./shell.css";

const SIDEBAR_STORAGE_KEY = "9gateway_sidebar_collapsed";

function getInitialCollapsedState(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return localStorage.getItem(SIDEBAR_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

export const AppShell: React.FC = () => {
  const [isCollapsed, setIsCollapsed] = useState<boolean>(getInitialCollapsedState);
  const [isMobileDrawerOpen, setIsMobileDrawerOpen] = useState(false);
  const { theme, toggleTheme } = useTheme();
  const location = useLocation();

  // Keep page title synchronized with active route
  usePageTitle();

  // Close mobile drawer on route change
  useEffect(() => {
    setIsMobileDrawerOpen(false);
  }, [location.pathname]);

  const handleToggleCollapse = () => {
    setIsCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(SIDEBAR_STORAGE_KEY, next ? "true" : "false");
      } catch {
        // Storage restricted
      }
      return next;
    });
  };

  return (
    <div className="gw-shell-container" data-testid="app-shell">
      {/* Accessible Skip to Main Content link */}
      <SkipLink />

      {/* Desktop Sidebar (visible on screens >= 768px) */}
      <Sidebar
        isCollapsed={isCollapsed}
        onToggleCollapse={handleToggleCollapse}
      />

      {/* Mobile Top Header (visible on screens < 768px) */}
      <header className="gw-mobile-header" data-testid="mobile-header">
        <IconButton
          icon={<Menu size={20} />}
          aria-label="Open navigation menu"
          aria-expanded={isMobileDrawerOpen}
          aria-controls="mobile-navigation-drawer"
          variant="ghost"
          size="md"
          onClick={() => setIsMobileDrawerOpen(true)}
          data-testid="mobile-menu-btn"
        />

        <Link to="/overview" className="gw-mobile-brand">
          <div className="gw-brand-icon-box" aria-hidden="true" style={{ width: 28, height: 28 }}>
            <Zap size={16} />
          </div>
          <span>9Gateway</span>
        </Link>

        <div className="gw-mobile-actions">
          <IconButton
            icon={theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}
            aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            variant="ghost"
            size="md"
            onClick={toggleTheme}
            data-testid="theme-toggle-mobile"
          />
        </div>
      </header>

      {/* Mobile Navigation Drawer */}
      <Drawer
        isOpen={isMobileDrawerOpen}
        onClose={() => setIsMobileDrawerOpen(false)}
        title="9Gateway Console"
        description="Operations navigation"
        placement="left"
      >
        <div id="mobile-navigation-drawer" className="gw-mobile-drawer-nav">
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              onClick={() => setIsMobileDrawerOpen(false)}
              className={({ isActive }) =>
                `gw-nav-link ${isActive ? "gw-nav-link--active" : ""}`
              }
            >
              <span className="gw-nav-icon" aria-hidden="true">
                {item.icon}
              </span>
              <span className="gw-nav-label">{item.label}</span>
            </NavLink>
          ))}
        </div>

        <div className="gw-mobile-drawer-footer">
          <div className="gw-sidebar-status-box">
            <div className="gw-sidebar-status-info">
              <span className="gw-sidebar-status-title">Proxy Status</span>
              <span style={{ fontSize: "var(--font-size-xs)", color: "var(--text-secondary)" }}>
                v0.1.0-dev
              </span>
            </div>
            <StatusPill label="Online" />
          </div>
        </div>
      </Drawer>

      {/* Main Content Frame */}
      <div className="gw-shell-main">
        <PageHeader />

        <main id="main-content" tabIndex={-1} className="gw-main-content">
          <RouteErrorBoundary>
            <React.Suspense fallback={<RouteLoadingFallback />}>
              <Outlet />
            </React.Suspense>
          </RouteErrorBoundary>
        </main>
      </div>
    </div>
  );
};
