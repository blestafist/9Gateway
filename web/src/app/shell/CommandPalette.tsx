import React, { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  Search,
  LayoutDashboard,
  BarChart3,
  KeyRound,
  ArrowLeftRight,
  Server,
  Sun,
  Moon,
  RefreshCw,
  LogOut,
  X,
} from "lucide-react";
import { useTheme } from "../../shared/theme";
import { useAuth } from "../../features/auth";

export interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
}

export interface CommandItem {
  id: string;
  label: string;
  description: string;
  category: "Navigation" | "Actions";
  icon: React.ReactNode;
  shortcut?: string;
  keywords: string[];
  action: () => void;
}

export const CommandPalette: React.FC<CommandPaletteProps> = ({ isOpen, onClose }) => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { theme, toggleTheme } = useTheme();
  const { isAuthenticated, logout } = useAuth();

  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);

  const inputRef = useRef<HTMLInputElement>(null);
  const previouslyFocusedElementRef = useRef<HTMLElement | null>(null);
  const listRef = useRef<HTMLUListElement>(null);

  // Focus preservation and lock
  useEffect(() => {
    if (!isOpen) return;

    previouslyFocusedElementRef.current = document.activeElement as HTMLElement | null;
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    // Set initial focus to search input
    const timer = setTimeout(() => {
      inputRef.current?.focus();
    }, 0);

    return () => {
      clearTimeout(timer);
      document.body.style.overflow = originalOverflow;
      if (
        previouslyFocusedElementRef.current &&
        typeof previouslyFocusedElementRef.current.focus === "function"
      ) {
        previouslyFocusedElementRef.current.focus();
      }
    };
  }, [isOpen]);

  // Reset state when opening/closing
  useEffect(() => {
    if (isOpen) {
      setQuery("");
      setSelectedIndex(0);
    }
  }, [isOpen]);

  // Static commands list - STRICTLY NO SERVER DATA, REQUEST BODIES, OR SECRETS
  const commands: CommandItem[] = useMemo(() => {
    const items: CommandItem[] = [
      {
        id: "cmd-nav-overview",
        label: "Go to Overview",
        description: "Gateway throughput, tokens, and operations status",
        category: "Navigation",
        icon: <LayoutDashboard size={18} aria-hidden="true" />,
        shortcut: "G O",
        keywords: ["overview", "dashboard", "home", "stats"],
        action: () => navigate("/overview"),
      },
      {
        id: "cmd-nav-usage",
        label: "Go to Usage & Analytics",
        description: "Time-series charts, breakdowns, and token costs",
        category: "Navigation",
        icon: <BarChart3 size={18} aria-hidden="true" />,
        shortcut: "G U",
        keywords: ["usage", "analytics", "tokens", "cost", "charts", "metrics"],
        action: () => navigate("/usage"),
      },
      {
        id: "cmd-nav-keys",
        label: "Go to API Keys",
        description: "Manage access tokens, key status, and authorization policies",
        category: "Navigation",
        icon: <KeyRound size={18} aria-hidden="true" />,
        shortcut: "G K",
        keywords: ["keys", "api keys", "tokens", "policy", "credentials"],
        action: () => navigate("/keys"),
      },
      {
        id: "cmd-nav-requests",
        label: "Go to Requests",
        description: "Inspect recent gateway requests, response codes, and latency",
        category: "Navigation",
        icon: <ArrowLeftRight size={18} aria-hidden="true" />,
        shortcut: "G R",
        keywords: ["requests", "logs", "traces", "history", "traffic"],
        action: () => navigate("/requests"),
      },
      {
        id: "cmd-nav-system",
        label: "Go to System Diagnostics",
        description: "Runtime health, SQLite persistence, upstream status, and build info",
        category: "Navigation",
        icon: <Server size={18} aria-hidden="true" />,
        shortcut: "G S",
        keywords: ["system", "diagnostics", "health", "uptime", "version", "readiness"],
        action: () => navigate("/system"),
      },
      {
        id: "cmd-action-theme",
        label: theme === "dark" ? "Switch to Light Theme" : "Switch to Dark Theme",
        description: "Toggle interface appearance between dark and light mode",
        category: "Actions",
        icon: theme === "dark" ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />,
        shortcut: "T",
        keywords: ["theme", "mode", "dark", "light", "color"],
        action: () => toggleTheme(),
      },
      {
        id: "cmd-action-refresh",
        label: "Refresh Current Page",
        description: "Refetch latest telemetry and data from the gateway",
        category: "Actions",
        icon: <RefreshCw size={18} aria-hidden="true" />,
        shortcut: "R",
        keywords: ["refresh", "reload", "update", "fetch"],
        action: () => {
          void queryClient.invalidateQueries();
          window.dispatchEvent(new CustomEvent("gateway-refresh"));
        },
      },
    ];

    if (isAuthenticated) {
      items.push({
        id: "cmd-action-logout",
        label: "Sign Out",
        description: "End current operator session and return to sign in",
        category: "Actions",
        icon: <LogOut size={18} aria-hidden="true" />,
        keywords: ["logout", "sign out", "exit"],
        action: () => {
          void logout();
        },
      });
    }

    return items;
  }, [navigate, queryClient, theme, toggleTheme, isAuthenticated, logout]);

  // Filter items matching query
  const filteredCommands = useMemo(() => {
    const trimmed = query.trim().toLowerCase();
    if (!trimmed) return commands;
    return commands.filter((cmd) => {
      return (
        cmd.label.toLowerCase().includes(trimmed) ||
        cmd.description.toLowerCase().includes(trimmed) ||
        cmd.category.toLowerCase().includes(trimmed) ||
        cmd.keywords.some((k) => k.toLowerCase().includes(trimmed))
      );
    });
  }, [commands, query]);

  // Keep selected index within bounds
  useEffect(() => {
    setSelectedIndex(0);
  }, [filteredCommands.length]);

  // Execute active command
  const handleExecute = useCallback(
    (command: CommandItem) => {
      onClose();
      command.action();
    },
    [onClose]
  );

  // Keyboard navigation within palette
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      onClose();
      return;
    }

    if (filteredCommands.length === 0) return;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((prev) => (prev + 1) % filteredCommands.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((prev) => (prev - 1 + filteredCommands.length) % filteredCommands.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const selected = filteredCommands[selectedIndex];
      if (selected) {
        handleExecute(selected);
      }
    }
  };

  if (!isOpen) return null;

  const selectedCommand = filteredCommands[selectedIndex];

  return (
    <div
      className="gw-command-palette-backdrop"
      onClick={onClose}
      data-testid="command-palette-backdrop"
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command Palette"
        className="gw-command-palette"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
        data-testid="command-palette"
      >
        {/* Search Input */}
        <div className="gw-command-palette-input-wrap">
          <Search size={18} className="gw-command-search-icon" aria-hidden="true" />
          <input
            ref={inputRef}
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-autocomplete="list"
            aria-controls="command-palette-results"
            aria-activedescendant={selectedCommand ? selectedCommand.id : undefined}
            placeholder="Type a command or search (e.g. 'theme', 'usage', 'requests')..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="gw-command-palette-input"
            data-testid="command-palette-input"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery("")}
              className="gw-command-input-clear-btn"
              aria-label="Clear search query"
            >
              <X size={16} aria-hidden="true" />
            </button>
          )}
        </div>

        {/* Live region for assistive technology announcements */}
        <div className="gw-sr-only" aria-live="polite" aria-atomic="true">
          {filteredCommands.length} command{filteredCommands.length === 1 ? "" : "s"} available.
        </div>

        {/* Command List Results */}
        <div className="gw-command-palette-body">
          {filteredCommands.length === 0 ? (
            <div className="gw-command-empty" role="status" data-testid="command-empty">
              No matching commands found for &ldquo;{query}&rdquo;
            </div>
          ) : (
            <ul
              id="command-palette-results"
              ref={listRef}
              role="listbox"
              aria-label="Available commands"
              className="gw-command-palette-list"
              data-testid="command-palette-list"
            >
              {filteredCommands.map((item, index) => {
                const isSelected = index === selectedIndex;
                return (
                  <li
                    key={item.id}
                    id={item.id}
                    role="option"
                    aria-selected={isSelected}
                    className={`gw-command-palette-item ${
                      isSelected ? "gw-command-palette-item--selected" : ""
                    }`}
                    onClick={() => handleExecute(item)}
                    onMouseEnter={() => setSelectedIndex(index)}
                    data-testid={`command-item-${item.id}`}
                  >
                    <div className="gw-command-item-left">
                      <div className="gw-command-item-icon" aria-hidden="true">
                        {item.icon}
                      </div>
                      <div className="gw-command-item-content">
                        <span className="gw-command-item-label">{item.label}</span>
                        <span className="gw-command-item-desc">{item.description}</span>
                      </div>
                    </div>
                    {item.shortcut && (
                      <div className="gw-command-item-shortcut">
                        <kbd className="gw-kbd-shortcut">{item.shortcut}</kbd>
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>

        {/* Footer with key usage hints */}
        <div className="gw-command-palette-footer">
          <div className="gw-command-footer-hints">
            <span><kbd className="gw-kbd-shortcut">↑</kbd> <kbd className="gw-kbd-shortcut">↓</kbd> to navigate</span>
            <span><kbd className="gw-kbd-shortcut">↵</kbd> to select</span>
            <span><kbd className="gw-kbd-shortcut">esc</kbd> to close</span>
          </div>
          <span className="gw-command-footer-scope">9Gateway console actions only</span>
        </div>
      </div>
    </div>
  );
};
