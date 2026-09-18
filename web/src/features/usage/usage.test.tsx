import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { UsagePage } from "./UsagePage";
import { calculateUsageDelta } from "./deltas";
import {
  computePresetBounds,
  computePreviousBounds,
  getValidBuckets,
  formatBucketLabel,
  normalizeBucketResolution,
} from "./ranges";
import {
  validateUsageTimeseriesResponse,
  validateUsageBreakdownResponse,
} from "./validation";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AuthContext, AuthContextValue } from "../auth";
import usageTimeseriesFixture from "./fixtures/usageTimeseries.fixture.json";
import usageBreakdownModelFixture from "./fixtures/usageBreakdownModel.fixture.json";
import usageBreakdownKeyFixture from "./fixtures/usageBreakdownKey.fixture.json";
import usageBreakdownOutcomeFixture from "./fixtures/usageBreakdownOutcome.fixture.json";
import usageNullGapsFixture from "./fixtures/usageNullGaps.fixture.json";
import usageEmptyFixture from "./fixtures/usageEmpty.fixture.json";

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

describe("T169 Usage Ranges and Bucket Resolutions", () => {
  it("computes bounded presets correctly", () => {
    const fixedNow = new Date("2026-09-18T12:00:00.000Z");

    // 1h
    const h1 = computePresetBounds("1h", fixedNow);
    expect(h1.before).toBe("2026-09-18T12:00:00.000Z");
    expect(h1.after).toBe("2026-09-18T11:00:00.000Z");

    // 24h
    const h24 = computePresetBounds("24h", fixedNow);
    expect(h24.after).toBe("2026-09-17T12:00:00.000Z");

    // 30d
    const d30 = computePresetBounds("30d", fixedNow);
    expect(d30.after).toBe("2026-08-19T12:00:00.000Z");

    // all retained (after MUST be undefined so server scans all retained SQLite records)
    const all = computePresetBounds("all", fixedNow);
    expect(all.after).toBeUndefined();
    expect(all.before).toBe("2026-09-18T12:00:00.000Z");
  });

  it("computes adjacent previous range bounds or returns null for all-retained", () => {
    // 24h window: previous should be non-overlapping adjacent 24h
    const currentBounds = {
      after: "2026-09-17T00:00:00.000Z",
      before: "2026-09-18T00:00:00.000Z",
    };
    const prev = computePreviousBounds(currentBounds);
    expect(prev).toBeDefined();
    expect(prev?.before).toBe("2026-09-17T00:00:00.000Z");
    expect(prev?.after).toBe("2026-09-16T00:00:00.000Z");

    // All retained has no after bound -> previous range must be null
    const allBounds = {
      before: "2026-09-18T00:00:00.000Z",
    };
    const prevAll = computePreviousBounds(allBounds);
    expect(prevAll).toBeNull();
  });

  it("restricts bucket resolutions based on range window duration", () => {
    // Short ranges (1h, 24h)
    expect(getValidBuckets("1h")).toEqual(["auto", "five_minutes", "hour"]);
    expect(getValidBuckets("24h")).toEqual(["auto", "five_minutes", "hour"]);

    // Medium ranges (7d, 30d)
    expect(getValidBuckets("30d")).toEqual(["auto", "hour", "day"]);

    // Long ranges (90d, 1y, all)
    expect(getValidBuckets("90d")).toEqual(["auto", "day", "week", "month"]);
    expect(getValidBuckets("all")).toEqual(["auto", "day", "week", "month"]);

    // Format labels
    expect(formatBucketLabel("auto")).toBe("Auto");
    expect(formatBucketLabel("five_minutes")).toBe("5 min");
    expect(formatBucketLabel("hour")).toBe("Hourly");
    expect(formatBucketLabel("day")).toBe("Daily");
  });

  it("normalizes unknown and over-detailed buckets to auto", () => {
    expect(normalizeBucketResolution("30d", "five_minutes")).toBe("auto");
    expect(normalizeBucketResolution("30d", "hour")).toBe("hour");
    expect(normalizeBucketResolution("custom", "not-a-bucket", 2 * 24 * 60 * 60 * 1000)).toBe("auto");
  });
});

describe("T169 Usage Deltas Calculation", () => {
  it("handles standard changes, zero baselines, and unknown periods", () => {
    const increase = calculateUsageDelta(1200, 1000);
    expect(increase.direction).toBe("increase");
    expect(increase.formattedPercentage).toBe("+20.0%");
    expect(increase.comparisonText).toBe("+20.0% vs. prev (1,000)");

    // Null previous baseline
    const nullPrev = calculateUsageDelta(500, null);
    expect(nullPrev.direction).toBe("unavailable");
    expect(nullPrev.isUnavailable).toBe(true);
    expect(nullPrev.comparisonText).toBe("Previous period unavailable");

    // Cost metric with estimation note
    const costDelta = calculateUsageDelta(5000000, 4000000, { isCost: true });
    expect(costDelta.comparisonText).toContain("vs. prev est.");
    expect(costDelta.comparisonText).toContain("$4.00");

    // Error metric with warning sentiment
    const errorDelta = calculateUsageDelta(10, 5, { isErrorMetric: true });
    expect(errorDelta.sentiment).toBe("warning");
  });
});

describe("T169 API Response Validators", () => {
  it("validates timeseries response exactly matching T168 schema", () => {
    const validated = validateUsageTimeseriesResponse(usageTimeseriesFixture);
    expect(validated.buckets.length).toBe(usageTimeseriesFixture.buckets.length);
    expect(validated.bucket).toBe("hour");
    expect(validated.retention_limited).toBe(false);
    expect(validated.effective_after).toBe("2026-09-17T00:00:00Z");
  });

  it("validates breakdown responses for models, keys, and outcomes", () => {
    const validatedModel = validateUsageBreakdownResponse(usageBreakdownModelFixture);
    expect(validatedModel.group_by).toBe("model");
    expect(validatedModel.rows.length).toBeGreaterThan(0);
    expect(validatedModel.other).toBeDefined();
    expect(validatedModel.total.total_requests).toBeGreaterThan(0);

    const validatedKey = validateUsageBreakdownResponse(usageBreakdownKeyFixture);
    expect(validatedKey.group_by).toBe("key");

    const validatedOutcome = validateUsageBreakdownResponse(usageBreakdownOutcomeFixture);
    expect(validatedOutcome.group_by).toBe("outcome");
  });
});

describe("T169 UsagePage Component and Lifecycle", () => {
  let queryClient: ReturnType<typeof createAdminQueryClient>;
  let fetchMock: ReturnType<typeof vi.fn>;

  const defaultAuthValue: AuthContextValue = {
    isAuthenticated: true,
    isLoading: false,
    csrfToken: "mock-csrf-token",
    idleExpiresAt: null,
    expiresAt: null,
    login: vi.fn().mockResolvedValue(undefined),
    logout: vi.fn().mockResolvedValue(undefined),
    checkSession: vi.fn().mockResolvedValue(true),
    expireSession: vi.fn(),
  };

  beforeEach(() => {
    queryClient = createAdminQueryClient();
    fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse(usageTimeseriesFixture);
      }
      if (url.includes("/admin/v1/usage/breakdown")) {
        if (url.includes("group_by=key")) {
          return mockJsonResponse(usageBreakdownKeyFixture);
        }
        if (url.includes("group_by=outcome")) {
          return mockJsonResponse(usageBreakdownOutcomeFixture);
        }
        return mockJsonResponse(usageBreakdownModelFixture);
      }
      return mockJsonResponse({});
    });
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    queryClient.clear();
  });

  function renderUsagePage(initialRoute = "/ui/usage") {
    return render(
      <MemoryRouter initialEntries={[initialRoute]}>
        <AuthContext.Provider value={defaultAuthValue}>
          <AdminQueryProvider client={queryClient}>
            <UsagePage />
          </AdminQueryProvider>
        </AuthContext.Provider>
      </MemoryRouter>
    );
  }

  it("renders full dashboard with <=5 KPI cards, primary chart, and rankings card", async () => {
    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("usage-page")).toBeInTheDocument();
      expect(screen.getByTestId("usage-kpi-grid")).toBeInTheDocument();
      expect(screen.getByTestId("primary-chart-card")).toBeInTheDocument();
      expect(screen.getByTestId("rankings-card")).toBeInTheDocument();
    });

    // Check 5 KPI cards
    expect(screen.getByTestId("kpi-requests")).toBeInTheDocument();
    expect(screen.getByTestId("kpi-tokens")).toBeInTheDocument();
    expect(screen.getByTestId("kpi-cost")).toBeInTheDocument();
    expect(screen.getByTestId("kpi-latency")).toBeInTheDocument();
    expect(screen.getByTestId("kpi-errors")).toBeInTheDocument();

    // Verify 30d preset is a first-class visible tab
    expect(screen.getByRole("button", { name: "30d" })).toBeInTheDocument();
  });

  it("restores preset, metric, and ranking dimension from URL search parameters", async () => {
    renderUsagePage("/ui/usage?range=30d&metric=cost&rank_dim=key&rank_metric=cost");

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "30d" })).toHaveAttribute("aria-pressed", "true");
      expect(
        within(screen.getByTestId("primary-chart-card")).getByRole("button", { name: "Cost" })
      ).toHaveAttribute("aria-pressed", "true");
      expect(
        within(screen.getByTestId("rankings-card")).getByRole("button", { name: "API Keys" })
      ).toHaveAttribute("aria-pressed", "true");
      expect(
        within(screen.getByTestId("rankings-card")).getByRole("button", { name: "Cost" })
      ).toHaveAttribute("aria-pressed", "true");
    });
  });

  it("renders only the selected chart and switches between metric tabs", async () => {
    renderUsagePage("/ui/usage?metric=tokens");

    await waitFor(() => {
      expect(screen.getByTestId("tokens-chart")).toBeInTheDocument();
    });

    expect(screen.queryByTestId("requests-chart")).not.toBeInTheDocument();
    expect(screen.queryByTestId("cost-chart")).not.toBeInTheDocument();
    expect(screen.queryByTestId("latency-chart")).not.toBeInTheDocument();

    // Switch to Requests metric view on primary chart card
    const chartCard = screen.getByTestId("primary-chart-card");
    await act(async () => {
      fireEvent.click(within(chartCard).getByRole("button", { name: "Requests" }));
    });

    await waitFor(() => {
      expect(screen.getByTestId("requests-chart")).toBeInTheDocument();
      expect(screen.queryByTestId("tokens-chart")).not.toBeInTheDocument();
    });
  });

  it("toggles accessible data table view on primary chart card", async () => {
    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("primary-chart-card")).toBeInTheDocument();
    });

    // Toggle to data table view
    const toggleBtn = screen.getByRole("button", { name: /switch to data table view/i });
    await act(async () => {
      fireEvent.click(toggleBtn);
    });

    await waitFor(() => {
      expect(screen.getByRole("region", { name: /data table view/i })).toBeInTheDocument();
      expect(screen.getByRole("table")).toBeInTheDocument();
    });
  });

  it("displays retention notice when retention_limited is true", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse(usageNullGapsFixture); // has retention_limited: true
      }
      return mockJsonResponse(usageBreakdownModelFixture);
    });

    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("retention-limited-notice")).toBeInTheDocument();
      expect(screen.getByText(/Retention Boundary Reached/i)).toBeInTheDocument();
    });
  });

  it("displays all-retained notice when 'all' preset is active", async () => {
    renderUsagePage("/ui/usage?range=all");

    await waitFor(() => {
      expect(screen.getByTestId("all-retained-notice")).toBeInTheDocument();
      expect(screen.getByText(/All Retained History/i)).toBeInTheDocument();
    });
  });

  it("renders unknown token telemetry as unavailable instead of zero", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse({
          ...usageNullGapsFixture,
          buckets: usageNullGapsFixture.buckets.map((bucket, index) =>
            index === 1
              ? { ...bucket, input_tokens: null, cached_input_tokens: null, output_tokens: null }
              : bucket
          ),
        });
      }
      return mockJsonResponse(usageBreakdownModelFixture);
    });

    renderUsagePage("/ui/usage?metric=tokens");

    await waitFor(() => {
      expect(screen.getByTestId("kpi-tokens")).toHaveTextContent("—");
    });
  });

  it("renders unavailable breakdown token totals when query data is absent", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse(usageTimeseriesFixture);
      }
      return mockJsonResponse({
        ...usageBreakdownModelFixture,
        other: undefined,
        total: undefined,
      });
    });

    renderUsagePage();

    const rankingsCard = screen.getByTestId("rankings-card");
    await waitFor(() => {
      expect(
        within(rankingsCard).getByRole("button", { name: /switch to detailed table view/i })
      ).toBeInTheDocument();
    });
    await act(async () => {
      fireEvent.click(within(rankingsCard).getByRole("button", { name: "Tokens" }));
      fireEvent.click(
        within(rankingsCard).getByRole("button", { name: /switch to detailed table view/i })
      );
    });

    await waitFor(() => {
      expect(screen.getByTestId("rankings-table")).toHaveTextContent("Unavailable");
    });
  });

  it("refreshes changed rolling bounds with one current and previous query set", async () => {
    vi.setSystemTime(new Date("2026-09-18T12:00:00.000Z"));
    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("usage-page")).toBeInTheDocument();
      expect(fetchMock).toHaveBeenCalledTimes(3);
    });

    const initialUrls = fetchMock.mock.calls.map(([input]) => String(input));
    vi.setSystemTime(new Date("2026-09-18T12:01:00.000Z"));

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /refresh/i }));
    });

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(6);
    });

    const refreshedUrls = fetchMock.mock.calls.map(([input]) => String(input));
    for (const initialUrl of initialUrls) {
      expect(refreshedUrls.filter((url) => url === initialUrl)).toHaveLength(1);
    }
    expect(refreshedUrls.slice(3)).toHaveLength(3);
    expect(refreshedUrls.slice(3)).not.toEqual(initialUrls);
  });

  it("handles null cost and latency null gaps gracefully without false zeros", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse(usageNullGapsFixture);
      }
      return mockJsonResponse(usageBreakdownModelFixture);
    });

    renderUsagePage("/ui/usage?metric=latency");

    await waitFor(() => {
      expect(screen.getByTestId("latency-chart")).toBeInTheDocument();
      // KPI cost card displays Unavailable
      expect(screen.getByTestId("kpi-cost")).toHaveTextContent("Unavailable");
    });
  });

  it("handles empty usage records without crashing", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse({ ...usageEmptyFixture, buckets: [] });
      }
      return mockJsonResponse({
        ...usageBreakdownModelFixture,
        rows: [],
        other: {
          id: "_other",
          name: "Other",
          is_unknown: false,
          is_deleted: false,
          total_requests: 0,
          successful_requests: 0,
          error_requests: 0,
          rejected_requests: 0,
          input_tokens: 0,
          cached_input_tokens: 0,
          output_tokens: 0,
          cost_micros: null,
        },
      });
    });

    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByText(/No Activity in Range/i)).toBeInTheDocument();
      expect(screen.getByText(/No Breakdown Data/i)).toBeInTheDocument();
    });
  });

  it("handles all-zero datasets in chart without crashing", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/admin/v1/usage/timeseries")) {
        return mockJsonResponse(usageEmptyFixture);
      }
      return mockJsonResponse(usageBreakdownModelFixture);
    });

    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("tokens-chart")).toBeInTheDocument();
      expect(screen.getByTestId("kpi-requests")).toHaveTextContent("0");
    });
  });

  it("renders 401 session expired state with login link", async () => {
    fetchMock.mockImplementation(async () => {
      return mockJsonResponse({ error: { message: "unauthorized" } }, { status: 401 });
    });

    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByText(/Session Expired/i)).toBeInTheDocument();
      expect(screen.getByRole("link", { name: /log in again/i })).toBeInTheDocument();
    });
  });

  it("renders 503 capacity constrained state with retry button", async () => {
    fetchMock.mockImplementation(async () => {
      return mockJsonResponse({ error: { message: "concurrency limit exceeded" } }, { status: 503 });
    });

    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByText(/Gateway Analytics Capacity Constrained/i)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /Retry Query/i })).toBeInTheDocument();
    });
  });

  it("handles custom UTC range input and validation", async () => {
    renderUsagePage("/ui/usage?range=custom");

    await waitFor(() => {
      expect(screen.getByTestId("custom-range-form")).toBeInTheDocument();
    });

    const startInput = screen.getByLabelText(/start timestamp in utc/i);
    const endInput = screen.getByLabelText(/end timestamp in utc/i);
    const applyBtn = screen.getByRole("button", { name: /apply range/i });

    // Try applying with invalid range (start after end)
    await act(async () => {
      fireEvent.change(startInput, { target: { value: "2026-09-18T10:00:00Z" } });
      fireEvent.change(endInput, { target: { value: "2026-09-17T10:00:00Z" } });
      fireEvent.click(applyBtn);
    });

    expect(screen.getByRole("alert")).toHaveTextContent(/must be strictly before/i);

    // Apply valid range
    await act(async () => {
      fireEvent.change(startInput, { target: { value: "2026-09-16T00:00:00Z" } });
      fireEvent.change(endInput, { target: { value: "2026-09-17T00:00:00Z" } });
      fireEvent.click(applyBtn);
    });

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("switches rankings dimension and view mode between bars and table", async () => {
    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("rankings-card")).toBeInTheDocument();
      expect(screen.getByTestId("rankings-bars")).toBeInTheDocument();
    });

    // Switch to Outcomes dimension
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Outcomes" }));
    });

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("group_by=outcome"),
        expect.anything()
      );
    });

    // Toggle to Table view in rankings card
    const viewTableBtn = screen.getByRole("button", { name: /switch to detailed table view/i });
    await act(async () => {
      fireEvent.click(viewTableBtn);
    });

    await waitFor(() => {
      expect(screen.getByTestId("rankings-table")).toBeInTheDocument();
      expect(screen.getByText("Total (untruncated)")).toBeInTheDocument();
    });
  });

  it("does not trigger automatic polling intervals on usage queries", async () => {
    renderUsagePage();

    await waitFor(() => {
      expect(screen.getByTestId("usage-page")).toBeInTheDocument();
    });

    const callsBefore = fetchMock.mock.calls.length;

    // Fast-forward 60 seconds
    await act(async () => {
      await new Promise((r) => setTimeout(r, 100));
    });

    // No background polling calls should happen
    expect(fetchMock.mock.calls.length).toBe(callsBefore);
  });
});
