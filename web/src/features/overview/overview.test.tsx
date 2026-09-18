import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { onlineManager } from "@tanstack/react-query";
import { OverviewPage } from "./OverviewPage";
import { calculateDelta } from "./deltas";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AuthContext, AuthContextValue } from "../auth";
import overviewFixture from "./fixtures/overview.fixture.json";
import overviewNullsFixture from "./fixtures/overviewNulls.fixture.json";
import overviewEmptyFixture from "./fixtures/overviewEmpty.fixture.json";

describe("T167 Overview Deltas Calculation", () => {
  it("handles zero baseline without infinity or misleading +100%", () => {
    // 0 to 0
    const zeroToZero = calculateDelta(0, 0);
    expect(zeroToZero.direction).toBe("unchanged");
    expect(zeroToZero.sentiment).toBe("neutral");
    expect(zeroToZero.percentage).toBeNull();
    expect(zeroToZero.formattedPercentage).toBeNull();
    expect(zeroToZero.isUnavailable).toBe(true);
    expect(zeroToZero.diff).toBe(0);
    expect(zeroToZero.comparisonText).toBe("0 in previous period");

    // 0 to 0 with isCost
    const zeroCost = calculateDelta(0, 0, { isCost: true });
    expect(zeroCost.comparisonText).toBe("0 in previous period (est.)");

    // 0 to 50: no baseline, marked unavailable, diff preserved
    const zeroToFifty = calculateDelta(50, 0);
    expect(zeroToFifty.direction).toBe("increase");
    expect(zeroToFifty.sentiment).toBe("neutral");
    expect(zeroToFifty.percentage).toBeNull();
    expect(zeroToFifty.isUnavailable).toBe(true);
    expect(zeroToFifty.diff).toBe(50);
    expect(zeroToFifty.comparisonText).toBe("No baseline (0 in prev period)");

    // 0 to 50 with warning semantics (error metric)
    const zeroToFiftyError = calculateDelta(50, 0, { isErrorMetric: true });
    expect(zeroToFiftyError.sentiment).toBe("warning");

    // 0 to 50 with isCost
    const zeroToFiftyCost = calculateDelta(50000, 0, { isCost: true });
    expect(zeroToFiftyCost.comparisonText).toBe("No baseline (0 in prev period, est.)");
  });

  it("handles unknown or null baselines cleanly", () => {
    const nullPrev = calculateDelta(100, null);
    expect(nullPrev.direction).toBe("unavailable");
    expect(nullPrev.sentiment).toBe("unavailable");
    expect(nullPrev.isUnavailable).toBe(true);
    expect(nullPrev.percentage).toBeNull();
    expect(nullPrev.comparisonText).toBe("Previous period unavailable");

    const nullCurrent = calculateDelta(null, 100);
    expect(nullCurrent.direction).toBe("unavailable");
    expect(nullCurrent.isUnavailable).toBe(true);

    const undefPrev = calculateDelta(100, undefined);
    expect(undefPrev.isUnavailable).toBe(true);
  });

  it("applies neutral sentiment to standard metrics on increase and decrease", () => {
    // Standard metric increase (requests, tokens, cost)
    const requestIncrease = calculateDelta(150, 100);
    expect(requestIncrease.direction).toBe("increase");
    expect(requestIncrease.sentiment).toBe("neutral");
    expect(requestIncrease.percentage).toBe(50);
    expect(requestIncrease.formattedPercentage).toBe("+50.0%");
    expect(requestIncrease.comparisonText).toBe("+50.0% vs. prev (100)");

    // Standard metric decrease
    const requestDecrease = calculateDelta(80, 100);
    expect(requestDecrease.direction).toBe("decrease");
    expect(requestDecrease.sentiment).toBe("neutral");
    expect(requestDecrease.percentage).toBe(-20);
    expect(requestDecrease.formattedPercentage).toBe("-20.0%");
    expect(requestDecrease.comparisonText).toBe("-20.0% vs. prev (100)");

    // Unchanged
    const unchanged = calculateDelta(100, 100);
    expect(unchanged.direction).toBe("unchanged");
    expect(unchanged.sentiment).toBe("neutral");
    expect(unchanged.percentage).toBe(0);
    expect(unchanged.formattedPercentage).toBe("0.0%");
  });

  it("applies warning semantics to errors and rejections on increase", () => {
    const errorIncrease = calculateDelta(15, 10, { isErrorMetric: true });
    expect(errorIncrease.direction).toBe("increase");
    expect(errorIncrease.sentiment).toBe("warning");
    expect(errorIncrease.formattedPercentage).toBe("+50.0%");

    const errorDecrease = calculateDelta(5, 10, { isErrorMetric: true });
    expect(errorDecrease.direction).toBe("decrease");
    expect(errorDecrease.sentiment).toBe("positive");
    expect(errorDecrease.formattedPercentage).toBe("-50.0%");
  });

  it("explicitly labels cost comparisons as estimates", () => {
    // 27990000 vs 22000000 micros ($27.99 vs $22.00)
    const costDelta = calculateDelta(27990000, 22000000, { isCost: true });
    expect(costDelta.direction).toBe("increase");
    expect(costDelta.sentiment).toBe("neutral");
    expect(costDelta.comparisonText).toContain("vs. prev est.");
    expect(costDelta.comparisonText).toContain("$22.00");
  });
});

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

describe("T167 OverviewPage Component and Lifecycle", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(overviewFixture));
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    onlineManager.setOnline(true);
    window.dispatchEvent(new Event("online"));
    Object.defineProperty(document, "visibilityState", {
      value: "visible",
      configurable: true,
    });
    vi.restoreAllMocks();
  });

  const createMockAuth = (overrides: Partial<AuthContextValue> = {}): AuthContextValue => ({
    isAuthenticated: true,
    isLoading: false,
    csrfToken: "mock-csrf",
    idleExpiresAt: null,
    expiresAt: null,
    login: vi.fn(),
    logout: vi.fn(),
    checkSession: vi.fn().mockResolvedValue(true),
    expireSession: vi.fn(),
    ...overrides,
  });

  const renderOverview = ({
    initialEntries = ["/ui/overview"],
    auth = createMockAuth(),
  } = {}) => {
    const queryClient = createAdminQueryClient();
    return {
      queryClient,
      ...render(
        <AuthContext.Provider value={auth}>
          <AdminQueryProvider client={queryClient}>
            <MemoryRouter initialEntries={initialEntries}>
              <OverviewPage />
            </MemoryRouter>
          </AdminQueryProvider>
        </AuthContext.Provider>
      ),
    };
  };

  it("renders full dashboard with KPI cards, health strip, and recent requests", async () => {
    renderOverview();

    // Skeletons while loading
    expect(screen.getByTestId("overview-loading-skeletons")).toBeInTheDocument();

    // Await data arrival
    await waitFor(() => {
      expect(screen.getByTestId("kpi-total-requests")).toBeInTheDocument();
    });

    // Check KPI values
    expect(screen.getByTestId("kpi-total-requests")).toHaveTextContent("150");
    expect(screen.getByTestId("kpi-active-requests")).toHaveTextContent("2");
    expect(screen.getByTestId("kpi-input-tokens")).toHaveTextContent("19,178,344");
    expect(screen.getByTestId("kpi-cached-tokens")).toHaveTextContent("11,381,601");
    expect(screen.getByTestId("kpi-output-tokens")).toHaveTextContent("72,803");
    expect(screen.getByTestId("kpi-estimated-cost")).toHaveTextContent("$27.99");
    expect(screen.getByTestId("kpi-errors-rejections")).toHaveTextContent("8");

    // Check Health Status Strip
    expect(screen.getByText("Gateway Health & Operations")).toBeInTheDocument();
    expect(screen.getByTestId("status-pipeline")).toBeInTheDocument();
    expect(screen.getByTestId("status-busy")).toHaveTextContent("2 Active");
    expect(screen.getByTestId("status-storage")).toHaveTextContent("5/6 Keys");

    // Check API Keys Summary
    expect(screen.getByTestId("key-summary-card")).toBeInTheDocument();
    expect(screen.getByText("Total Configured")).toBeInTheDocument();
    expect(screen.getByText("Active / Enabled")).toBeInTheDocument();

    // Check Deep Navigation Links
    expect(screen.getByRole("link", { name: /system diagnostics/i })).toHaveAttribute(
      "href",
      "/ui/system"
    );
    expect(screen.getByRole("link", { name: /view all requests/i })).toHaveAttribute(
      "href",
      "/ui/requests"
    );
    expect(screen.getByRole("link", { name: /manage api keys/i })).toHaveAttribute(
      "href",
      "/ui/keys"
    );
    expect(screen.getByRole("link", { name: /view usage analytics/i })).toHaveAttribute(
      "href",
      "/ui/usage"
    );

    // Check textual freshness
    expect(screen.getByTestId("freshness-indicator")).toHaveTextContent(/updated at/i);
  });

  it("handles null and unknown usage/cost without crash or misleading zero", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(overviewNullsFixture));

    renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("kpi-input-tokens")).toBeInTheDocument();
    });

    // Unknown metrics show honest placeholder "—"
    expect(screen.getByTestId("kpi-input-tokens")).toHaveTextContent("—");
    expect(screen.getByTestId("kpi-cached-tokens")).toHaveTextContent("—");
    expect(screen.getByTestId("kpi-output-tokens")).toHaveTextContent("—");
    expect(screen.getByTestId("kpi-estimated-cost")).toHaveTextContent("—");
  });

  it("renders empty state when there is no traffic recorded", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(overviewEmptyFixture));

    renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("kpi-total-requests")).toBeInTheDocument();
    });

    expect(screen.getByText("No Recent Requests")).toBeInTheDocument();
    expect(screen.getByText(/0 total requests in window/i)).toBeInTheDocument();
  });

  it("handles period preset changes with bounded parameters", async () => {
    renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("kpi-total-requests")).toBeInTheDocument();
    });

    // Initial 24h query had no after/before in URL query string
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringMatching(/\/admin\/v1\/overview(?:\?|$)/),
      expect.anything()
    );

    // Switch to 7d
    const tab7d = screen.getByRole("button", { name: "7d" });
    fireEvent.click(tab7d);

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        expect.stringMatching(/\/admin\/v1\/overview\?after=.*&before=.*/),
        expect.anything()
      );
    });

    // Switch back to 24h
    const tab24h = screen.getByRole("button", { name: "24h" });
    fireEvent.click(tab24h);

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        expect.stringMatching(/\/admin\/v1\/overview$/),
        expect.anything()
      );
    });
  });

  it("preserves stale data and displays warning when background refresh fails", async () => {
    // 1. Initial success, then subsequent refresh fails with 500
    globalThis.fetch = vi
      .fn()
      .mockImplementationOnce(async () => mockJsonResponse(overviewFixture))
      .mockImplementationOnce(async () =>
        mockJsonResponse(
          { error: { code: "internal_error", message: "Database temporarily busy" } },
          { status: 500 }
        )
      );

    renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("kpi-total-requests")).toHaveTextContent("150");
    });

    const refreshBtn = screen.getByTestId("overview-refresh-btn");
    fireEvent.click(refreshBtn);

    // Snapshot is preserved: previous data is still rendered
    await waitFor(() => {
      expect(screen.getByText(/Background Refresh Failed/i)).toBeInTheDocument();
    });
    expect(screen.getByTestId("kpi-total-requests")).toHaveTextContent("150");
    expect(screen.getByTestId("freshness-indicator")).toHaveTextContent("Stale snapshot — refresh failed");
  });

  it("renders expired session alert with login action on 401", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () =>
      mockJsonResponse(
        { error: { code: "unauthorized", message: "Session expired" } },
        { status: 401 }
      )
    );

    renderOverview();

    await waitFor(() => {
      expect(screen.getByText("Session Expired")).toBeInTheDocument();
    });
    expect(screen.getByRole("link", { name: /log in/i })).toHaveAttribute("href", "/ui/login");
  });

  it("renders capacity constrained alert on 503", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () =>
      mockJsonResponse(
        { error: { code: "service_unavailable", message: "Gateway capacity constrained" } },
        { status: 503, headers: { "Retry-After": "1" } }
      )
    );

    renderOverview();

    await waitFor(() => {
      expect(screen.getByText("Analytics Engine Capacity Constrained")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });

  it("stops polling when tab is hidden or offline and updates freshness text", async () => {
    renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("freshness-indicator")).toBeInTheDocument();
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
    onlineManager.setOnline(true);
  });

  it("manual refresh does not trigger overlapping requests while fetch is in progress", async () => {
    // 1. Initial success
    globalThis.fetch = vi.fn().mockImplementation(async () => mockJsonResponse(overviewFixture));
    const { queryClient } = renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("overview-refresh-btn")).toBeInTheDocument();
    });

    // 2. Setup deferred pending response for background refresh
    let resolvePending: (value: Response) => void;
    const pendingPromise = new Promise<Response>((resolve) => {
      resolvePending = resolve;
    });

    globalThis.fetch = vi.fn().mockImplementation(() => pendingPromise);

    const refreshBtn = screen.getByTestId("overview-refresh-btn");
    fireEvent.click(refreshBtn);

    // Refresh button is now in-flight and disabled
    await waitFor(() => {
      expect(refreshBtn).toBeDisabled();
    });

    const callCount = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length;

    // Trigger second click while in-flight
    fireEvent.click(refreshBtn);

    // Must not create overlapping request
    expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callCount);

    // Resolve in-flight request
    await act(async () => {
      resolvePending!(mockJsonResponse(overviewFixture));
    });

    await waitFor(() => {
      expect(refreshBtn).not.toBeDisabled();
    });

    queryClient.clear();
  });

  it("handles long model and key strings with truncation and title attributes", async () => {
    const longModelRequest = {
      ...overviewFixture.recent_requests[0]!,
      request_id: "long-id-12345",
      model: "anthropic.claude-3-5-sonnet-20241022-v2:0-extremely-long-identifier-string",
      api_key_name: "production-billing-and-telemetry-pipeline-api-key-with-long-name",
    };

    globalThis.fetch = vi.fn().mockImplementation(async () =>
      mockJsonResponse({
        ...overviewFixture,
        recent_requests: [longModelRequest],
      })
    );

    const { queryClient } = renderOverview();

    await waitFor(() => {
      expect(screen.getByTestId("recent-request-row-long-id-12345")).toBeInTheDocument();
    });

    const modelCell = screen.getByTitle(
      "anthropic.claude-3-5-sonnet-20241022-v2:0-extremely-long-identifier-string"
    );
    expect(modelCell).toHaveClass("gw-truncate-cell");

    const keyCell = screen.getByTitle(
      "production-billing-and-telemetry-pipeline-api-key-with-long-name"
    );
    expect(keyCell).toHaveClass("gw-truncate-cell");

    queryClient.clear();
  });
});
