import React, { useState, useEffect, useRef } from "react";
import { Outlet, NavLink, Link, useLocation } from "react-router-dom";
import { Menu, Zap, Sun, Moon, Search } from "lucide-react";
import { Drawer, IconButton, StatusPill } from "../../shared/ui";
import { useTheme } from "../../shared/theme";
import { SkipLink } from "./SkipLink";
import { Sidebar, NAV_ITEMS } from "./Sidebar";
import { PageHeader } from "./PageHeader";
import { RouteErrorBoundary } from "./RouteErrorBoundary";
import { RouteLoadingFallback } from "./RouteLoadingFallback";
import { CommandPalette } from "./CommandPalette";
import { useGlobalShortcuts } from "./useGlobalShortcuts";
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
  const [isCommandPaletteOpen, setIsCommandPaletteOpen] = useState(false);
  const [isOnline, setIsOnline] = useState<boolean>(() =>
    typeof navigator !== "undefined" ? navigator.onLine : true
  );

  const mobileMenuTriggerRef = useRef<HTMLButtonElement>(null);
  const { theme, toggleTheme } = useTheme();
  const location = useLocation();

  // Monitor connectivity state
  useEffect(() => {
    const handleOnline = () => setIsOnline(true);
    const handleOffline = () => setIsOnline(false);

    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);

    return () => {
      window.removeEventListener("online", handleOnline);
      window.removeEventListener("offline", handleOffline);
    };
  }, []);

  // Safe global keyboard shortcuts (Command Palette, chords, theme, help)
  useGlobalShortcuts({
    onOpenCommandPalette: () => setIsCommandPaletteOpen(true),
    isCommandPaletteOpen,
  });

  // Scroll and focus restoration on route change
  useEffect(() => {
    if (
      typeof window !== "undefined" &&
      typeof window.scrollTo === "function" &&
      !navigator.userAgent?.includes("jsdom")
    ) {
      try {
        window.scrollTo(0, 0);
      } catch {
        // Restricted environment
      }
    }
    const mainContent = document.getElementById("main-content");
    if (mainContent) {
      mainContent.scrollTop = 0;
      if (!document.querySelector(".gw-dialog-backdrop, .gw-drawer-backdrop, .gw-command-palette-backdrop")) {
        mainContent.focus({ preventScroll: true });
      }
    }
  }, [location.pathname]);

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
        isOnline={isOnline}
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
          ref={mobileMenuTriggerRef}
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
            icon={<Search size={18} />}
            aria-label="Open Command Palette"
            variant="ghost"
            size="md"
            onClick={() => setIsCommandPaletteOpen(true)}
            data-testid="mobile-command-palette-btn"
          />

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
        restoreFocusTo={mobileMenuTriggerRef}
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
            <StatusPill
              label={isOnline ? "Online" : "Offline"}
              variant={isOnline ? undefined : "warning"}
            />
          </div>
        </div>
      </Drawer>

      {/* Main Content Frame */}
      <div className="gw-shell-main">
        <PageHeader
          onOpenCommandPalette={() => setIsCommandPaletteOpen(true)}
          isOnline={isOnline}
        />

        <main id="main-content" tabIndex={-1} className="gw-main-content">
          <RouteErrorBoundary>
            <React.Suspense fallback={<RouteLoadingFallback />}>
              <Outlet />
            </React.Suspense>
          </RouteErrorBoundary>
        </main>
      </div>

      {/* Accessible Command & Search Palette */}
      <CommandPalette
        isOpen={isCommandPaletteOpen}
        onClose={() => setIsCommandPaletteOpen(false)}
      />
    </div>
  );
};
