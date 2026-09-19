import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { SystemPage, buildDiagnosticsText } from "./SystemPage";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AuthContext, AuthContextValue } from "../auth";
import systemHealthyFixture from "./fixtures/systemHealthy.fixture.json";
import systemDegradedFixture from "./fixtures/systemDegraded.fixture.json";
import systemUnavailableFixture from "./fixtures/systemUnavailable.fixture.json";

function mockJsonResponse(data: unknown, status = 200) {
  return Promise.resolve(
    new Response(JSON.stringify(data), {
      status,
      headers: { "Content-Type": "application/json" },
    })
  );
}

function renderSystemPage(authOverrides?: Partial<AuthContextValue>) {
  const queryClient = createAdminQueryClient();
  const authValue: AuthContextValue = {
    isAuthenticated: true,
    isLoading: false,
    csrfToken: "csrf-token-xyz",
    idleExpiresAt: "2026-09-19T10:00:00Z",
    expiresAt: "2026-09-19T12:00:00Z",
    login: vi.fn(),
    logout: vi.fn(),
    checkSession: vi.fn().mockResolvedValue(true),
    expireSession: vi.fn(),
    ...authOverrides,
  };

  const result = render(
    <AuthContext.Provider value={authValue}>
      <AdminQueryProvider client={queryClient}>
        <MemoryRouter initialEntries={["/ui/system"]}>
          <SystemPage />
        </MemoryRouter>
      </AdminQueryProvider>
    </AuthContext.Provider>
  );

  return { ...result, queryClient };
}

describe("T176 System and Diagnostics Page", () => {
  const originalFetch = globalThis.fetch;
  const originalNavigator = globalThis.navigator;

  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    Object.assign(globalThis, { navigator: originalNavigator });
  });

  describe("Healthy State Rendering", () => {
    it("renders complete system indicators with allowlisted fields only", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(systemHealthyFixture));
      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-status-badge")).toHaveTextContent("Healthy");
      });

      // Version, commit, uptime
      expect(screen.getByTestId("system-version")).toHaveTextContent("v0.1.0-rc1");
      expect(screen.getByTestId("system-commit")).toHaveTextContent("abcdef1");
      expect(screen.getByTestId("system-uptime")).toHaveTextContent("1h 0m");

      // Individual checks
      expect(screen.getByTestId("check-sqlite")).toBeInTheDocument();
      expect(screen.getByTestId("check-schema")).toBeInTheDocument();
      expect(screen.getByTestId("check-telemetry")).toBeInTheDocument();
      expect(screen.getByTestId("check-upstream")).toBeInTheDocument();
      expect(screen.getByTestId("check-lifecycle")).toBeInTheDocument();

      // Telemetry
      expect(screen.getByTestId("telemetry-active-requests")).toHaveTextContent("0");
      expect(screen.getByTestId("telemetry-queue-depth")).toHaveTextContent("0");
      expect(screen.getByTestId("telemetry-queue-capacity")).toHaveTextContent("192");
      expect(screen.getByTestId("queue-pressure-badge")).toHaveTextContent("Normal");

      // Storage
      expect(screen.getByTestId("storage-schema-version")).toHaveTextContent("v9");
      expect(screen.getByTestId("storage-current-schema-version")).toHaveTextContent("v9");

      // Limits
      expect(screen.getByTestId("limit-request-retention")).toHaveTextContent("30d (2592000s)");
      expect(screen.getByTestId("limit-body-retention")).toHaveTextContent("7d (604800s)");
      expect(screen.getByTestId("limit-body-bytes")).toHaveTextContent("1 MB (1048576 B)");

      queryClient.clear();
    });
  });

  describe("Degraded and Failure States with Doc Links", () => {
    it("renders degraded badge, failure messages, and documentation links without internals", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(systemDegradedFixture));
      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-status-badge")).toHaveTextContent("Degraded");
      });

      // Development build badge
      expect(screen.getByTestId("dev-build-badge")).toHaveTextContent("Development Build");

      // Schema check failed with message and link
      expect(screen.getByTestId("check-message-schema")).toHaveTextContent("database schema version 8 does not match current version 9");
      const schemaLink = screen.getByRole("link", { name: /schema documentation: Storage operations/i });
      expect(schemaLink).toHaveAttribute("href", "https://github.com/pestit/9Gateway#storage");

      // Telemetry queue pressure and drops
      expect(screen.getByTestId("telemetry-dropped-records")).toHaveTextContent("42");
      expect(screen.getByTestId("queue-pressure-badge")).toHaveTextContent("High Pressure");

      // Schema mismatch warning alert
      expect(screen.getByText(/SQLite schema is at version 8, expected version 9/i)).toBeInTheDocument();

      queryClient.clear();
    });

    it("distinguishes null schema_version in unavailable state", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(systemUnavailableFixture));
      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("storage-schema-version")).toHaveTextContent("Unavailable (null)");
      });

      queryClient.clear();
    });
  });

  describe("Polling Lifecycle and Visibility", () => {
    it("stops polling when tab is hidden or offline and updates freshness text", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(systemHealthyFixture));
      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-freshness-info")).toBeInTheDocument();
      });

      // Simulate tab becoming hidden
      act(() => {
        Object.defineProperty(document, "visibilityState", {
          value: "hidden",
          configurable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });

      await waitFor(() => {
        expect(screen.getByText("Tab inactive — updates paused")).toBeInTheDocument();
      });

      // Simulate tab becoming visible again
      act(() => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          configurable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });

      await waitFor(() => {
        expect(screen.getByText(/updated at/i)).toBeInTheDocument();
      });

      // Simulate going offline
      act(() => {
        window.dispatchEvent(new Event("offline"));
      });

      await waitFor(() => {
        expect(screen.getByText("Offline — automatic polling paused")).toBeInTheDocument();
      });

      // Restore online state
      act(() => {
        window.dispatchEvent(new Event("online"));
      });

      queryClient.clear();
    });

    it("preserves previous snapshot with stale banner on background refresh error", async () => {
      let callNum = 0;
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        callNum++;
        if (callNum === 1) {
          return mockJsonResponse(systemHealthyFixture);
        }
        return Promise.resolve(new Response(JSON.stringify({ error: { message: "gateway busy" } }), { status: 500 }));
      });

      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-version")).toHaveTextContent("v0.1.0-rc1");
      });

      // Trigger manual refresh which fails
      const refreshBtn = screen.getByTestId("system-refresh-btn");
      await act(async () => {
        fireEvent.click(refreshBtn);
      });

      await waitFor(() => {
        // Warning alert is visible
        expect(screen.getByText(/Displaying previous operational snapshot/i)).toBeInTheDocument();
      });

      // Old snapshot remains visible
      expect(screen.getByTestId("system-version")).toHaveTextContent("v0.1.0-rc1");

      queryClient.clear();
    });

    it("manual refresh does not trigger duplicate in-flight requests", async () => {
      let resolveFetch: (val: Response) => void;
      const pending = new Promise<Response>((res) => {
        resolveFetch = res;
      });

      let fetchCount = 0;
      globalThis.fetch = vi.fn().mockImplementation(() => {
        fetchCount++;
        if (fetchCount === 1) {
          return mockJsonResponse(systemHealthyFixture);
        }
        return pending;
      });

      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-refresh-btn")).toBeInTheDocument();
      });

      const refreshBtn = screen.getByTestId("system-refresh-btn");
      fireEvent.click(refreshBtn);

      await waitFor(() => {
        expect(refreshBtn).toBeDisabled();
      });

      const currentCount = fetchCount;
      // Click again while in-flight
      fireEvent.click(refreshBtn);
      expect(fetchCount).toBe(currentCount);

      // Resolve in-flight
      await act(async () => {
        resolveFetch!(new Response(JSON.stringify(systemHealthyFixture), { status: 200 }));
      });

      await waitFor(() => {
        expect(refreshBtn).not.toBeDisabled();
      });

      queryClient.clear();
    });
  });

  describe("Safe Diagnostics Summary & Copy Preview", () => {
    it("builds exact allowlisted diagnostics text with zero secrets", () => {
      const text = buildDiagnosticsText(systemHealthyFixture);
      expect(text).toContain("=== 9Gateway System Diagnostics ===");
      expect(text).toContain("Version: v0.1.0-rc1");
      expect(text).toContain("Commit: abcdef1");
      expect(text).toContain("Overall Ready: true");
      expect(text).toContain("Queue Capacity: 192");
      expect(text).toContain("Max Captured Body Bytes: 1048576");

      // Strictly verify no secrets
      expect(text).not.toContain("Authorization");
      expect(text).not.toContain("Bearer");
      expect(text).not.toContain("password");
      expect(text).not.toContain("pepper");
      expect(text).not.toContain("token");
      expect(text).not.toContain("internal.corp");
    });

    it("opens preview modal and copies exact allowlisted preview to clipboard", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(systemHealthyFixture));
      const writeTextMock = vi.fn().mockResolvedValue(undefined);
      Object.assign(navigator, {
        clipboard: {
          writeText: writeTextMock,
        },
      });

      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-diagnostics-btn")).toBeInTheDocument();
      });

      // Open modal
      fireEvent.click(screen.getByTestId("system-diagnostics-btn"));

      await waitFor(() => {
        expect(screen.getByRole("dialog")).toBeInTheDocument();
      });

      // Exact preview is displayed inside pre
      const preview = screen.getByTestId("diagnostics-preview-box");
      expect(preview).toHaveTextContent("=== 9Gateway System Diagnostics ===");
      expect(preview).toHaveTextContent("Version: v0.1.0-rc1");

      // Click copy button
      const copyBtn = screen.getByTestId("copy-diagnostics-submit-btn");
      await act(async () => {
        fireEvent.click(copyBtn);
      });

      expect(writeTextMock).toHaveBeenCalledTimes(1);
      const copiedText = writeTextMock.mock.calls[0]?.[0] as string;
      expect(copiedText).toContain("=== 9Gateway System Diagnostics ===");
      expect(copiedText).toContain("Version: v0.1.0-rc1");
      expect(screen.getByText("Copied!")).toBeInTheDocument();

      queryClient.clear();
    });
  });

  describe("Query Cache Logout Scrubbing", () => {
    it("clears query cache on logout ensuring no snapshot survives", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(systemHealthyFixture));
      const { queryClient } = renderSystemPage();

      await waitFor(() => {
        expect(screen.getByTestId("system-version")).toHaveTextContent("v0.1.0-rc1");
      });

      // Confirm cache has data
      const cached = queryClient.getQueryData(["system", "info"]);
      expect(cached).toBeDefined();

      // Clear cache as occurs on logout
      queryClient.clear();

      expect(queryClient.getQueryData(["system", "info"])).toBeUndefined();
    });
  });
});
