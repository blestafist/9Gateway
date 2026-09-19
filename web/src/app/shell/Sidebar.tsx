import React from "react";
import { NavLink, Link } from "react-router-dom";
import {
  LayoutDashboard,
  BarChart3,
  KeyRound,
  ArrowLeftRight,
  Server,
  ChevronLeft,
  ChevronRight,
  Zap,
} from "lucide-react";
import { Tooltip, StatusPill } from "../../shared/ui";

export interface NavItem {
  path: string;
  label: string;
  icon: React.ReactNode;
}

export const NAV_ITEMS: NavItem[] = [
  { path: "/overview", label: "Overview", icon: <LayoutDashboard size={18} /> },
  { path: "/usage", label: "Usage", icon: <BarChart3 size={18} /> },
  { path: "/keys", label: "API Keys", icon: <KeyRound size={18} /> },
  { path: "/requests", label: "Requests", icon: <ArrowLeftRight size={18} /> },
  { path: "/system", label: "System", icon: <Server size={18} /> },
];

export interface SidebarProps {
  isCollapsed: boolean;
  onToggleCollapse: () => void;
  onNavigate?: () => void;
  isOnline?: boolean;
}

export const Sidebar: React.FC<SidebarProps> = ({
  isCollapsed,
  onToggleCollapse,
  onNavigate,
  isOnline = true,
}) => {
  return (
    <aside
      className={`gw-sidebar ${isCollapsed ? "gw-sidebar--collapsed" : ""}`}
      aria-label="Sidebar navigation"
      data-testid="sidebar"
    >
      {/* Brand Header */}
      <div className="gw-sidebar-brand">
        <Link
          to="/overview"
          className="gw-brand-logo-area"
          title="9Gateway Console Home"
          onClick={onNavigate}
        >
          <div className="gw-brand-icon-box" aria-hidden="true">
            <Zap size={18} />
          </div>
          {!isCollapsed && (
            <div className="gw-brand-text">
              <span className="gw-brand-name">9Gateway</span>
              <div className="gw-brand-badge-line">
                <span className="gw-brand-version">v0.1.0-dev</span>
              </div>
            </div>
          )}
        </Link>
      </div>

      {/* Main Navigation */}
      <nav className="gw-sidebar-nav" aria-label="Main Navigation">
        {NAV_ITEMS.map((item) => {
          const link = (
            <NavLink
              key={item.path}
              to={item.path}
              onClick={onNavigate}
              className={({ isActive }) =>
                `gw-nav-link ${isActive ? "gw-nav-link--active" : ""}`
              }
            >
              <span className="gw-nav-icon" aria-hidden="true">
                {item.icon}
              </span>
              <span className="gw-nav-label">{item.label}</span>
            </NavLink>
          );

          if (isCollapsed) {
            return (
              <Tooltip
                key={item.path}
                content={item.label}
                side="right"
              >
                {link}
              </Tooltip>
            );
          }

          return link;
        })}
      </nav>

      {/* Sidebar Footer & Reserved Connection Area */}
      <div className="gw-sidebar-footer">
        {!isCollapsed && (
          <div className="gw-sidebar-status-box" data-testid="sidebar-status-area">
            <div className="gw-sidebar-status-info">
              <span className="gw-sidebar-status-title">Status</span>
              <span style={{ fontSize: "var(--font-size-xs)", color: "var(--text-secondary)" }}>
                Policy Proxy
              </span>
            </div>
            <StatusPill
              label={isOnline ? "Online" : "Offline"}
              variant={isOnline ? undefined : "warning"}
            />
          </div>
        )}

        <button
          type="button"
          onClick={onToggleCollapse}
          aria-label={isCollapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-expanded={!isCollapsed}
          className="gw-sidebar-collapse-btn"
          data-testid="sidebar-toggle-btn"
        >
          {isCollapsed ? (
            <ChevronRight size={16} aria-hidden="true" />
          ) : (
            <>
              <ChevronLeft size={16} aria-hidden="true" />
              <span className="gw-sidebar-collapse-text">Collapse Sidebar</span>
            </>
          )}
        </button>
      </div>
    </aside>
  );
};
