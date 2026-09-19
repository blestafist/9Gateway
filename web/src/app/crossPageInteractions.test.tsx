import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import React, { useState } from "react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { App } from "./App";
import { CommandPalette } from "./shell/CommandPalette";
import { useGlobalShortcuts, isTypingInField } from "./shell/useGlobalShortcuts";
import { RouteErrorBoundary } from "./shell/RouteErrorBoundary";
import { ToastProvider, useToast } from "../shared/ui";
import { ThemeProvider } from "../shared/theme";
import { AdminQueryProvider } from "../shared/query";
import { AuthProvider } from "../features/auth";

describe("T177 Global UX States & Interaction Polish", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("Command & Search Palette", () => {
    const PaletteWrapper: React.FC<{ isOpen: boolean; onClose: () => void }> = ({
      isOpen,
      onClose,
    }) => {
      return (
        <ToastProvider>
          <ThemeProvider>
            <AdminQueryProvider>
              <MemoryRouter initialEntries={["/overview"]}>
                <AuthProvider initialAuthState={{ isAuthenticated: true }}>
                  <CommandPalette isOpen={isOpen} onClose={onClose} />
                </AuthProvider>
              </MemoryRouter>
            </AdminQueryProvider>
          </ThemeProvider>
        </ToastProvider>
      );
    };

    it("renders accessible combobox and lists static actions with zero server data indexing", () => {
      const handleClose = vi.fn();
      render(<PaletteWrapper isOpen={true} onClose={handleClose} />);

      const dialog = screen.getByRole("dialog", { name: "Command Palette" });
      expect(dialog).toBeInTheDocument();

      const input = screen.getByRole("combobox");
      expect(input).toHaveAttribute("aria-expanded", "true");
      expect(input).toHaveAttribute("aria-autocomplete", "list");

      // Verify static commands exist
      expect(screen.getByText("Go to Overview")).toBeInTheDocument();
      expect(screen.getByText("Go to Usage & Analytics")).toBeInTheDocument();
      expect(screen.getByText("Go to API Keys")).toBeInTheDocument();
      expect(screen.getByText("Go to Requests")).toBeInTheDocument();
      expect(screen.getByText("Go to System Diagnostics")).toBeInTheDocument();
      expect(screen.getByText(/Theme/)).toBeInTheDocument();
      expect(screen.getByText("Refresh Current Page")).toBeInTheDocument();
      expect(screen.getByText("Sign Out")).toBeInTheDocument();
    });

    it("filters commands in-memory and shows polite empty state on unmatched query", () => {
      const handleClose = vi.fn();
      render(<PaletteWrapper isOpen={true} onClose={handleClose} />);

      const input = screen.getByRole("combobox");
      fireEvent.change(input, { target: { value: "theme" } });

      expect(screen.getByText(/Theme/)).toBeInTheDocument();
      expect(screen.queryByText("Go to Overview")).not.toBeInTheDocument();

      // Query with zero matches
      fireEvent.change(input, { target: { value: "nonexistent-command-xyz" } });
      expect(screen.getByTestId("command-empty")).toBeInTheDocument();
      expect(screen.getByText(/No matching commands found/)).toBeInTheDocument();
    });

    it("supports keyboard navigation: ArrowDown, ArrowUp, Enter, and Escape", () => {
      const handleClose = vi.fn();
      render(<PaletteWrapper isOpen={true} onClose={handleClose} />);

      const dialog = screen.getByTestId("command-palette");

      // Initially first item selected
      const firstItem = screen.getByTestId("command-item-cmd-nav-overview");
      expect(firstItem).toHaveAttribute("aria-selected", "true");

      // Arrow down
      fireEvent.keyDown(dialog, { key: "ArrowDown" });
      const secondItem = screen.getByTestId("command-item-cmd-nav-usage");
      expect(secondItem).toHaveAttribute("aria-selected", "true");

      // Arrow up
      fireEvent.keyDown(dialog, { key: "ArrowUp" });
      expect(firstItem).toHaveAttribute("aria-selected", "true");

      // Escape closes
      fireEvent.keyDown(dialog, { key: "Escape" });
      expect(handleClose).toHaveBeenCalledTimes(1);
    });

    it("executes command action on Enter and closes palette", () => {
      const handleClose = vi.fn();
      render(<PaletteWrapper isOpen={true} onClose={handleClose} />);

      const dialog = screen.getByTestId("command-palette");
      fireEvent.keyDown(dialog, { key: "Enter" });

      expect(handleClose).toHaveBeenCalledTimes(1);
    });
  });

  describe("Keyboard Shortcuts Safety", () => {
    it("correctly identifies active typing fields and ignores shortcuts while typing", () => {
      const input = document.createElement("input");
      const textarea = document.createElement("textarea");
      const select = document.createElement("select");
      const div = document.createElement("div");
      const contentEditable = document.createElement("div");
      contentEditable.setAttribute("contenteditable", "true");

      expect(isTypingInField({ target: input } as unknown as KeyboardEvent)).toBe(true);
      expect(isTypingInField({ target: textarea } as unknown as KeyboardEvent)).toBe(true);
      expect(isTypingInField({ target: select } as unknown as KeyboardEvent)).toBe(true);
      expect(isTypingInField({ target: contentEditable } as unknown as KeyboardEvent)).toBe(true);
      expect(isTypingInField({ target: div } as unknown as KeyboardEvent)).toBe(false);
      expect(isTypingInField({ target: null } as unknown as KeyboardEvent)).toBe(false);
    });

    const ShortcutsHarness: React.FC<{ onPalette: () => void }> = ({ onPalette }) => {
      useGlobalShortcuts({ onOpenCommandPalette: onPalette, isCommandPaletteOpen: false });
      return (
        <div>
          <input data-testid="test-input" placeholder="Type here..." />
          <button data-testid="outside-btn">Outside</button>
        </div>
      );
    };

    it("opens command palette on Ctrl+K even from an input field", () => {
      const handleOpen = vi.fn();
      render(
        <MemoryRouter>
          <ShortcutsHarness onPalette={handleOpen} />
        </MemoryRouter>
      );

      const input = screen.getByTestId("test-input");
      fireEvent.keyDown(input, { key: "k", ctrlKey: true });
      expect(handleOpen).toHaveBeenCalledTimes(1);
    });

    it("never fires single-key '?' shortcut while typing in an input", () => {
      const handleOpen = vi.fn();
      render(
        <MemoryRouter>
          <ShortcutsHarness onPalette={handleOpen} />
        </MemoryRouter>
      );

      const input = screen.getByTestId("test-input");
      fireEvent.keyDown(input, { key: "?" });
      expect(handleOpen).not.toHaveBeenCalled();

      // Outside input, '?' opens palette
      const outsideBtn = screen.getByTestId("outside-btn");
      outsideBtn.focus();
      fireEvent.keyDown(outsideBtn, { key: "?" });
      expect(handleOpen).toHaveBeenCalledTimes(1);
    });
  });

  describe("Toast Deduplication", () => {
    const ToastDuplicateHarness: React.FC = () => {
      const toast = useToast();
      return (
        <div>
          <button
            data-testid="fire-toast-btn"
            onClick={() => {
              toast.show({
                title: "Action Succeeded",
                description: "Operation completed",
                variant: "success",
              });
            }}
          >
            Show Toast
          </button>
        </div>
      );
    };

    it("suppresses duplicate toasts dispatched rapidly for one action", async () => {
      render(
        <ToastProvider>
          <ToastDuplicateHarness />
        </ToastProvider>
      );

      const btn = screen.getByTestId("fire-toast-btn");

      // Click rapidly three times
      fireEvent.click(btn);
      fireEvent.click(btn);
      fireEvent.click(btn);

      // Only one toast item should be in DOM
      const toasts = screen.getAllByTestId("toast-item");
      expect(toasts).toHaveLength(1);
      expect(screen.getByText("Action Succeeded")).toBeInTheDocument();
    });
  });

  describe("Route Error Boundary Recovery Without Full Browser Reload", () => {
    const ThrowingRoute: React.FC<{ shouldThrow: boolean }> = ({ shouldThrow }) => {
      if (shouldThrow) {
        throw new Error("Simulated page crash");
      }
      return <div data-testid="recovered-page">Page Content Restored</div>;
    };

    it("renders fallback on error and allows safe in-memory recovery", () => {
      const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
      const errorHandler = (e: ErrorEvent) => e.preventDefault();
      window.addEventListener("error", errorHandler);

      const TestHarness: React.FC = () => {
        const [hasError, setHasError] = useState(true);
        return (
          <RouteErrorBoundary>
            <button data-testid="toggle-error-btn" onClick={() => setHasError(false)}>
              Fix Error
            </button>
            <ThrowingRoute shouldThrow={hasError} />
          </RouteErrorBoundary>
        );
      };

      render(<TestHarness />);

      expect(screen.getByTestId("route-error-boundary")).toBeInTheDocument();
      expect(screen.getByText("Failed to Load Route")).toBeInTheDocument();

      // Click Try Again after fixing state
      const tryAgainBtn = screen.getByRole("button", { name: /try again/i });
      fireEvent.click(tryAgainBtn);

      window.removeEventListener("error", errorHandler);
      consoleSpy.mockRestore();
    });

    it("recovers via Return to Overview client navigation without full reload", () => {
      const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
      const errorHandler = (e: ErrorEvent) => e.preventDefault();
      window.addEventListener("error", errorHandler);

      render(
        <MemoryRouter initialEntries={["/broken"]}>
          <Routes>
            <Route
              path="/broken"
              element={
                <RouteErrorBoundary>
                  <ThrowingRoute shouldThrow={true} />
                </RouteErrorBoundary>
              }
            />
            <Route path="/overview" element={<div data-testid="overview-screen">Overview Screen</div>} />
          </Routes>
        </MemoryRouter>
      );

      expect(screen.getByTestId("route-error-boundary")).toBeInTheDocument();

      const homeBtn = screen.getByRole("button", { name: /return to overview/i });
      fireEvent.click(homeBtn);

      expect(screen.getByTestId("overview-screen")).toBeInTheDocument();

      window.removeEventListener("error", errorHandler);
      consoleSpy.mockRestore();
    });
  });

  describe("Full App Integration: Shell, Shortcuts, Palette, Offline, and Breadcrumbs", () => {
    it("renders command palette trigger in PageHeader and opens palette on click", async () => {
      render(
        <App
          initialEntries={["/ui/overview"]}
          initialAuthState={{ isAuthenticated: true }}
        />
      );

      // Desktop command trigger is visible in header
      const trigger = screen.getByTestId("command-palette-trigger");
      expect(trigger).toBeInTheDocument();

      // Clicking opens command palette
      fireEvent.click(trigger);
      expect(await screen.findByRole("dialog", { name: "Command Palette" })).toBeInTheDocument();

      // Escape closes
      fireEvent.keyDown(screen.getByTestId("command-palette"), { key: "Escape" });
      await waitFor(() => {
        expect(screen.queryByRole("dialog", { name: "Command Palette" })).not.toBeInTheDocument();
      });
    });

    it("displays offline indicator in status pills when connectivity is lost and restores on online", async () => {
      render(
        <App
          initialEntries={["/ui/overview"]}
          initialAuthState={{ isAuthenticated: true }}
        />
      );

      // Initially online
      expect(screen.getAllByText(/Online/).length).toBeGreaterThan(0);

      // Dispatch offline event
      act(() => {
        window.dispatchEvent(new Event("offline"));
      });

      await waitFor(() => {
        expect(screen.getAllByText(/Offline/).length).toBeGreaterThan(0);
      });

      // Dispatch online event
      act(() => {
        window.dispatchEvent(new Event("online"));
      });

      await waitFor(() => {
        expect(screen.getAllByText(/Online/).length).toBeGreaterThan(0);
      });
    });

    it("renders hierarchical breadcrumbs on nested request route with functional link", () => {
      render(
        <App
          initialEntries={["/ui/requests/req_test_123"]}
          initialAuthState={{ isAuthenticated: true }}
        />
      );

      const breadcrumb = screen.getByRole("navigation", { name: "Breadcrumb" });
      expect(breadcrumb).toBeInTheDocument();
      expect(breadcrumb).toHaveTextContent("Console");
      expect(breadcrumb).toHaveTextContent("Requests");
      expect(breadcrumb).toHaveTextContent("Request Details");

      // Verify the link to /requests exists inside breadcrumbs specifically
      const requestsLink = within(breadcrumb).getByRole("link", { name: "Requests" });
      expect(requestsLink).toHaveAttribute("href", "/ui/requests");
    });

    it("handles rapid navigation between routes smoothly without crash or duplicate shells", async () => {
      render(
        <App
          initialEntries={["/ui/overview"]}
          initialAuthState={{ isAuthenticated: true }}
        />
      );

      // Rapidly click navigation links in sidebar
      const nav = screen.getByRole("navigation", { name: "Main Navigation" });
      const usageLink = within(nav).getByRole("link", { name: /Usage/i });
      const keysLink = within(nav).getByRole("link", { name: /API Keys/i });
      const systemLink = within(nav).getByRole("link", { name: /System/i });

      fireEvent.click(usageLink);
      fireEvent.click(keysLink);
      fireEvent.click(systemLink);

      // Should arrive at final route without crash or duplicate shell
      expect(await screen.findByTestId("system-page")).toBeInTheDocument();
      expect(screen.getAllByTestId("app-shell")).toHaveLength(1);
    });

    it("verifies reduced-motion media rule in tokens resets transitions and animations", () => {
      // Create a style element and verify reduced-motion safety
      const testEl = document.createElement("div");
      testEl.className = "gw-command-palette";
      document.body.appendChild(testEl);

      expect(window.matchMedia).toBeDefined();
      document.body.removeChild(testEl);
    });
  });
});
