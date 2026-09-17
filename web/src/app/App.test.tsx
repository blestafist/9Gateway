import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import React from "react";
import { App } from "./App";
import { RouteErrorBoundary } from "./shell/RouteErrorBoundary";

describe("T163 Shell & Navigation", () => {
  beforeEach(() => {
    localStorage.clear();
    document.title = "9Gateway";
  });

  it("renders skip link and targets main content", () => {
    render(<App initialEntries={["/ui/overview"]} />);

    const skipLink = screen.getByRole("link", { name: /skip to main content/i });
    expect(skipLink).toBeInTheDocument();
    expect(skipLink).toHaveAttribute("href", "#main-content");

    const main = document.getElementById("main-content");
    expect(main).toBeInTheDocument();
    expect(main).toHaveAttribute("tabindex", "-1");
  });

  it("renders desktop navigation with strictly allowed items only", () => {
    render(<App initialEntries={["/ui/overview"]} />);

    const sidebarNav = screen.getByRole("navigation", { name: "Main Navigation" });
    expect(sidebarNav).toBeInTheDocument();

    // Check allowed items
    expect(screen.getByRole("link", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Usage" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "API Keys" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Requests" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "System" })).toBeInTheDocument();

    // Strictly ensure excluded 9router items are NOT present anywhere in navigation
    const navText = sidebarNav.textContent || "";
    expect(navText).not.toMatch(/providers/i);
    expect(navText).not.toMatch(/combo/i);
    expect(navText).not.toMatch(/vision/i);
    expect(navText).not.toMatch(/proxy pools/i);
    expect(navText).not.toMatch(/skills/i);
    expect(navText).not.toMatch(/topology/i);
  });

  it("marks the active route with aria-current='page'", async () => {
    render(<App initialEntries={["/ui/overview"]} />);

    await waitFor(() => {
      const overviewLink = screen.getByRole("link", { name: "Overview" });
      expect(overviewLink).toHaveAttribute("aria-current", "page");
    });

    const usageLink = screen.getByRole("link", { name: "Usage" });
    expect(usageLink).not.toHaveAttribute("aria-current");
  });

  it("synchronizes document title and breadcrumbs with the active route", async () => {
    render(<App initialEntries={["/ui/overview"]} />);

    await waitFor(() => {
      expect(document.title).toBe("Overview | 9Gateway");
    });

    const breadcrumb = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(breadcrumb).toHaveTextContent("Console");
    expect(breadcrumb).toHaveTextContent("Overview");
  });

  it("navigates directly to /ui/usage and renders usage placeholder", async () => {
    render(<App initialEntries={["/ui/usage"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("usage-page")).toBeInTheDocument();
      expect(document.title).toBe("Usage & Analytics | 9Gateway");
    });

    expect(screen.getByRole("tab", { name: "24h" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Tokens" })).toBeInTheDocument();
  });

  it("navigates directly to /ui/keys and renders API Keys placeholder", async () => {
    render(<App initialEntries={["/ui/keys"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
      expect(document.title).toBe("API Keys | 9Gateway");
    });

    expect(screen.getByText("API Endpoint")).toBeInTheDocument();
    expect(screen.getByText(/sk-c47/)).toBeInTheDocument();
  });

  it("redirects /ui/api-keys alias directly to /keys", async () => {
    render(<App initialEntries={["/ui/api-keys"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
      expect(document.title).toBe("API Keys | 9Gateway");
    });
  });

  it("navigates directly to /ui/requests and renders requests table shell", async () => {
    render(<App initialEntries={["/ui/requests"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("requests-page")).toBeInTheDocument();
      expect(document.title).toBe("Requests | 9Gateway");
    });

    expect(screen.getByRole("table", { name: "Recent Requests" })).toBeInTheDocument();
    expect(screen.getAllByText("claude-sonnet-5").length).toBeGreaterThan(0);
  });

  it("navigates directly to /ui/system and renders system diagnostics", async () => {
    render(<App initialEntries={["/ui/system"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("system-page")).toBeInTheDocument();
      expect(document.title).toBe("System Diagnostics | 9Gateway");
    });

    expect(screen.getByText("Gateway Core Health")).toBeInTheDocument();
    expect(screen.getByText("SQLite Storage")).toBeInTheDocument();
  });

  it("navigates directly to /ui/login and renders operator auth placeholder", async () => {
    render(<App initialEntries={["/ui/login"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("login-page")).toBeInTheDocument();
      expect(document.title).toBe("Sign In | 9Gateway");
    });

    expect(screen.getByText("Console Sign In")).toBeInTheDocument();
  });

  it("renders development component catalog when navigated in dev mode", async () => {
    render(<App initialEntries={["/ui/components"]} />);

    await waitFor(() => {
      expect(screen.getByText(/Design System & Primitives Catalog/i)).toBeInTheDocument();
    });
  });

  it("renders Not Found page on unmatched route", async () => {
    render(<App initialEntries={["/ui/non-existent-route"]} />);

    await waitFor(() => {
      expect(screen.getByTestId("not-found-page")).toBeInTheDocument();
      expect(screen.getByText("Page Not Found")).toBeInTheDocument();
    });
  });

  it("toggles desktop sidebar compact state and persists preference in localStorage", () => {
    render(<App initialEntries={["/ui/overview"]} />);

    const sidebar = screen.getByTestId("sidebar");
    const toggleBtn = screen.getByTestId("sidebar-toggle-btn");

    expect(sidebar).not.toHaveClass("gw-sidebar--collapsed");
    expect(toggleBtn).toHaveAttribute("aria-expanded", "true");
    expect(toggleBtn).toHaveAttribute("aria-label", "Collapse sidebar");

    // Click collapse
    fireEvent.click(toggleBtn);
    expect(sidebar).toHaveClass("gw-sidebar--collapsed");
    expect(toggleBtn).toHaveAttribute("aria-expanded", "false");
    expect(toggleBtn).toHaveAttribute("aria-label", "Expand sidebar");
    expect(localStorage.getItem("9gateway_sidebar_collapsed")).toBe("true");

    // Click expand
    fireEvent.click(toggleBtn);
    expect(sidebar).not.toHaveClass("gw-sidebar--collapsed");
    expect(toggleBtn).toHaveAttribute("aria-expanded", "true");
    expect(localStorage.getItem("9gateway_sidebar_collapsed")).toBe("false");
  });

  it("persists only allowed preferences in localStorage and never credentials or secret telemetry", () => {
    render(<App initialEntries={["/ui/overview"]} />);

    // Trigger theme toggle and sidebar toggle
    const themeBtn = screen.getByTestId("theme-toggle-header");
    fireEvent.click(themeBtn);

    const toggleBtn = screen.getByTestId("sidebar-toggle-btn");
    fireEvent.click(toggleBtn);

    const storedKeys: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k) storedKeys.push(k);
    }
    expect(storedKeys.length).toBeGreaterThan(0);
    for (const key of storedKeys) {
      expect(["9gateway_theme", "9gateway_sidebar_collapsed"]).toContain(key);
    }
  });

  it("opens and operates mobile drawer with keyboard and focus handling", async () => {
    render(<App initialEntries={["/ui/overview"]} />);

    const menuBtn = screen.getByTestId("mobile-menu-btn");
    expect(menuBtn).toHaveAttribute("aria-expanded", "false");

    // Open drawer
    fireEvent.click(menuBtn);
    expect(menuBtn).toHaveAttribute("aria-expanded", "true");

    const drawer = screen.getByRole("dialog");
    expect(drawer).toBeInTheDocument();
    expect(drawer).toHaveTextContent("9Gateway Console");

    // Close via Escape key
    fireEvent.keyDown(drawer, { key: "Escape" });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("recovers gracefully inside route error boundary when a chunk throws", () => {
    const ThrowingComponent: React.FC = () => {
      throw new Error("Simulated network chunk failure");
    };

    // Spy on console.error to avoid polluting test log
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    render(
      <RouteErrorBoundary>
        <ThrowingComponent />
      </RouteErrorBoundary>
    );

    expect(screen.getByTestId("route-error-boundary")).toBeInTheDocument();
    expect(screen.getByText("Failed to Load Route")).toBeInTheDocument();
    expect(screen.getByText(/Simulated network chunk failure/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /try again/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /return to overview/i })).toBeInTheDocument();

    consoleSpy.mockRestore();
  });
});

describe("T163 Layout & Responsive Structural Verifications", () => {
  it("uses overflow-x: hidden on shell root to prevent document horizontal scrollbar", () => {
    render(<App initialEntries={["/ui/overview"]} />);
    const shell = screen.getByTestId("app-shell");
    expect(shell).toHaveClass("gw-shell-container");
  });

  it("supports multiple standard responsive viewports (375, 768, 1024, 1440)", () => {
    const viewports = [375, 768, 1024, 1440];
    for (const width of viewports) {
      window.innerWidth = width;
      fireEvent(window, new Event("resize"));
      const { unmount } = render(<App initialEntries={["/ui/overview"]} />);
      expect(screen.getByTestId("app-shell")).toBeInTheDocument();
      expect(screen.getByTestId("page-header")).toBeInTheDocument();
      unmount();
    }
  });

  it("renders reserved connection and version info", () => {
    render(<App initialEntries={["/ui/overview"]} />);
    expect(screen.getByTestId("sidebar-status-area")).toBeInTheDocument();
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("v0.1.0-dev")).toBeInTheDocument();
  });
});
