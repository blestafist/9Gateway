import { render, screen, fireEvent, waitFor, within, act } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { onlineManager } from "@tanstack/react-query";
import { RequestsPage } from "./RequestsPage";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AdminKeyListItem } from "../keys";
import { AdminRequestListItem } from "./types";
import requestListFixture from "./fixtures/requestList.fixture.json";

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

const mockKeys: AdminKeyListItem[] = [
  {
    id: "key-prod-01",
    name: "Production Bot Key",
    display_prefix: "sk-prod",
    enabled: true,
    created_at: "2026-09-01T12:00:00Z",
    updated_at: "2026-09-01T12:00:00Z",
    expires_at: null,
    policy_summary: {
      allow_models: true,
      deny_models: false,
      log_request_body: true,
      log_response_body: false,
    },
  },
  {
    id: "key-dev-02",
    name: "Dev Playground Key",
    display_prefix: "sk-dev2",
    enabled: true,
    created_at: "2026-09-02T12:00:00Z",
    updated_at: "2026-09-02T12:00:00Z",
    expires_at: null,
    policy_summary: {
      allow_models: false,
      deny_models: false,
      log_request_body: false,
      log_response_body: false,
    },
  },
];

const mockOutcomesRequests: AdminRequestListItem[] = [
  {
    request_id: "req-outcome-complete",
    api_key_id: "key-prod-01",
    api_key_name: "Production Bot Key",
    method: "POST",
    path: "/v1/chat/completions",
    route: "/v1/chat/completions",
    model: "gpt-4o",
    requested_mode: "stream",
    upstream_mode: "stream",
    delivered_mode: "stream",
    downstream_status: 200,
    upstream_status: 200,
    terminal_outcome: "complete",
    upstream_started: true,
    error_code: null,
    client_bytes: 1024,
    upstream_bytes: 2048,
    delivered_bytes: 2048,
    input_tokens: 150,
    output_tokens: 250,
    total_tokens: 400,
    cached_input_tokens: 50,
    reasoning_output_tokens: 0,
    cost_micros: 5000,
    started_at: "2026-09-17T10:00:00Z",
    upstream_started_at: "2026-09-17T10:00:00.010Z",
    upstream_headers_at: "2026-09-17T10:00:00.100Z",
    first_byte_at: "2026-09-17T10:00:00.105Z",
    finished_at: "2026-09-17T10:00:01.000Z",
    total_micros: 1000000,
    time_to_upstream_headers_micros: 90000,
    time_to_first_byte_micros: 95000,
    stream_close_delay_micros: 5000,
  },
  {
    request_id: "req-outcome-pre-upstream",
    api_key_id: "key-prod-01",
    api_key_name: "Production Bot Key",
    method: "POST",
    path: "/v1/chat/completions",
    route: "/v1/chat/completions",
    model: "claude-3-5-sonnet",
    requested_mode: "non-stream",
    upstream_mode: null,
    delivered_mode: "non-stream",
    downstream_status: 400,
    upstream_status: null,
    terminal_outcome: "pre_upstream",
    upstream_started: false,
    error_code: "invalid_request",
    client_bytes: 512,
    upstream_bytes: 0,
    delivered_bytes: 128,
    input_tokens: null,
    output_tokens: null,
    total_tokens: null,
    cached_input_tokens: null,
    reasoning_output_tokens: null,
    cost_micros: null,
    started_at: "2026-09-17T10:01:00Z",
    upstream_started_at: null,
    upstream_headers_at: null,
    first_byte_at: null,
    finished_at: "2026-09-17T10:01:00.010Z",
    total_micros: 10000,
    time_to_upstream_headers_micros: null,
    time_to_first_byte_micros: null,
    stream_close_delay_micros: null,
  },
  {
    request_id: "req-outcome-rate-limited",
    api_key_id: "key-dev-02",
    api_key_name: "Dev Playground Key",
    method: "POST",
    path: "/v1/chat/completions",
    route: "/v1/chat/completions",
    model: "gpt-4o",
    requested_mode: "non-stream",
    upstream_mode: null,
    delivered_mode: "non-stream",
    downstream_status: 429,
    upstream_status: null,
    terminal_outcome: "rate_limited",
    upstream_started: false,
    error_code: "rate_limit_exceeded",
    client_bytes: 512,
    upstream_bytes: 0,
    delivered_bytes: 128,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cached_input_tokens: 0,
    reasoning_output_tokens: 0,
    cost_micros: 0,
    started_at: "2026-09-17T10:02:00Z",
    upstream_started_at: null,
    upstream_headers_at: null,
    first_byte_at: null,
    finished_at: "2026-09-17T10:02:00.005Z",
    total_micros: 5000,
    time_to_upstream_headers_micros: null,
    time_to_first_byte_micros: null,
    stream_close_delay_micros: null,
  },
  {
    request_id: "req-outcome-upstream-error",
    api_key_id: null,
    api_key_name: null,
    method: "POST",
    path: "/v1/chat/completions",
    route: "/v1/chat/completions",
    model: null,
    requested_mode: "stream",
    upstream_mode: "stream",
    delivered_mode: "stream",
    downstream_status: 502,
    upstream_status: 500,
    terminal_outcome: "upstream_error",
    upstream_started: true,
    error_code: "upstream_error",
    client_bytes: 1024,
    upstream_bytes: 256,
    delivered_bytes: 256,
    input_tokens: null,
    output_tokens: null,
    total_tokens: null,
    cached_input_tokens: null,
    reasoning_output_tokens: null,
    cost_micros: null,
    started_at: "2026-09-17T10:03:00Z",
    upstream_started_at: "2026-09-17T10:03:00.010Z",
    upstream_headers_at: "2026-09-17T10:03:00.200Z",
    first_byte_at: "2026-09-17T10:03:00.205Z",
    finished_at: "2026-09-17T10:03:00.210Z",
    total_micros: 210000,
    time_to_upstream_headers_micros: 190000,
    time_to_first_byte_micros: 195000,
    stream_close_delay_micros: 5000,
  },
];

const mockPage1Requests: AdminRequestListItem[] = Array.from({ length: 25 }, (_, i) => ({
  request_id: `0191eb0b62bc7b7489a2434685ef3b${String(i).padStart(2, "0")}`,
  api_key_id: "key-prod-01",
  api_key_name: "Production Bot Key",
  method: "POST",
  path: "/v1/chat/completions",
  route: "/v1/chat/completions",
  model: "gpt-4o",
  requested_mode: "stream",
  upstream_mode: "stream",
  delivered_mode: "stream",
  downstream_status: 200,
  upstream_status: 200,
  terminal_outcome: "complete",
  upstream_started: true,
  error_code: null,
  client_bytes: 1000 + i,
  upstream_bytes: 4000 + i,
  delivered_bytes: 4000 + i,
  input_tokens: 100 + i,
  output_tokens: 200 + i,
  total_tokens: 300 + (2 * i),
  cached_input_tokens: 50,
  reasoning_output_tokens: 0,
  cost_micros: 5000 + (100 * i),
  started_at: "2026-09-17T14:30:00Z",
  upstream_started_at: "2026-09-17T14:30:00.010Z",
  upstream_headers_at: "2026-09-17T14:30:00.100Z",
  first_byte_at: "2026-09-17T14:30:00.105Z",
  finished_at: "2026-09-17T14:30:01.000Z",
  total_micros: 1000000 + (10000 * i),
  time_to_upstream_headers_micros: 90000,
  time_to_first_byte_micros: 95000,
  stream_close_delay_micros: 5000,
}));

const mockPage2Requests: AdminRequestListItem[] = Array.from({ length: 25 }, (_, i) => ({
  request_id: `0191eb0b62bc7b7489a2434685ef3c${String(i).padStart(2, "0")}`,
  api_key_id: "key-dev-02",
  api_key_name: "Dev Playground Key",
  method: "POST",
  path: "/v1/chat/completions",
  route: "/v1/chat/completions",
  model: "claude-3-5-sonnet",
  requested_mode: "non-stream",
  upstream_mode: "non-stream",
  delivered_mode: "non-stream",
  downstream_status: 200,
  upstream_status: 200,
  terminal_outcome: "complete",
  upstream_started: true,
  error_code: null,
  client_bytes: 2000 + i,
  upstream_bytes: 8000 + i,
  delivered_bytes: 8000 + i,
  input_tokens: 200 + i,
  output_tokens: 400 + i,
  total_tokens: 600 + (2 * i),
  cached_input_tokens: 100,
  reasoning_output_tokens: 0,
  cost_micros: 10000 + (100 * i),
  started_at: "2026-09-17T13:30:00Z",
  upstream_started_at: "2026-09-17T13:30:00.010Z",
  upstream_headers_at: "2026-09-17T13:30:00.100Z",
  first_byte_at: "2026-09-17T13:30:00.105Z",
  finished_at: "2026-09-17T13:30:01.000Z",
  total_micros: 1200000 + (10000 * i),
  time_to_upstream_headers_micros: 90000,
  time_to_first_byte_micros: 95000,
  stream_close_delay_micros: 5000,
}));

function renderRequestsPage(initialUrl = "/requests") {
  const queryClient = createAdminQueryClient();
  return render(
    <AdminQueryProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialUrl]}>
        <Routes>
          <Route path="/requests" element={<RequestsPage />} />
          <Route path="/requests/:id" element={<div data-testid="request-detail-target">Detail Page</div>} />
          <Route path="/keys/:id" element={<div data-testid="key-detail-target">Key Detail Page</div>} />
          <Route path="/login" element={<div data-testid="login-target">Login Page</div>} />
        </Routes>
      </MemoryRouter>
    </AdminQueryProvider>
  );
}

describe("T173 Request History Explorer", () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    onlineManager.setOnline(true);
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    onlineManager.setOnline(true);
    vi.restoreAllMocks();
  });

  it("renders empty state when there are no requests recorded", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: [] }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-empty-state")).toBeInTheDocument();
    });

    expect(
      screen.getByText(/no completed requests have been recorded yet/i)
    ).toBeInTheDocument();
  });

  it("renders dense desktop table and mobile cards with complete metadata", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse(requestListFixture));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
      expect(screen.getByTestId("requests-cards-view")).toBeInTheDocument();
    });

    // Check request items rendered
    expect(screen.getAllByText("gpt-4o").length).toBeGreaterThan(0);
    expect(screen.getAllByText("claude-3-5-sonnet").length).toBeGreaterThan(0);

    // Desktop table heads must NOT look sortable (no sort icons, no aria-sort)
    const table = screen.getByRole("table", { name: "Recent Requests" });
    expect(table).toBeInTheDocument();

    const headers = within(table).getAllByRole("columnheader");
    for (const h of headers) {
      expect(h).not.toHaveAttribute("aria-sort");
    }
  });

  it("handles cursor pagination across multiple pages without accumulation or duplicates", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        if (url.includes("cursor=page2cursor")) {
          return Promise.resolve(
            mockJsonResponse({
              requests: mockPage2Requests,
              next_cursor: undefined,
            })
          );
        }
        return Promise.resolve(
          mockJsonResponse({
            requests: mockPage1Requests,
            next_cursor: "page2cursor",
          })
        );
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    // Page 1 assertions
    await waitFor(() => {
      expect(screen.getByTestId("request-row-0191eb0b62bc7b7489a2434685ef3b00")).toBeInTheDocument();
    });

    const nextBtn = screen.getByRole("button", { name: /go to next page/i });
    const prevBtn = screen.getByRole("button", { name: /go to previous page/i });

    expect(nextBtn).not.toBeDisabled();
    expect(prevBtn).toBeDisabled();
    expect(screen.getByText("Page 1 • 25 requests on page")).toBeInTheDocument();

    // Navigate to Page 2
    fireEvent.click(nextBtn);

    await waitFor(() => {
      expect(screen.getByTestId("request-row-0191eb0b62bc7b7489a2434685ef3c00")).toBeInTheDocument();
    });

    // Verify Page 1 requests are no longer present (no duplicate accumulation)
    expect(
      screen.queryByTestId("request-row-0191eb0b62bc7b7489a2434685ef3b00")
    ).not.toBeInTheDocument();

    expect(screen.getByText("Page 2 • 25 requests on page")).toBeInTheDocument();
    expect(nextBtn).toBeDisabled();
    expect(prevBtn).not.toBeDisabled();

    // Navigate back to Page 1
    fireEvent.click(prevBtn);

    await waitFor(() => {
      expect(screen.getByTestId("request-row-0191eb0b62bc7b7489a2434685ef3b00")).toBeInTheDocument();
    });

    expect(
      screen.queryByTestId("request-row-0191eb0b62bc7b7489a2434685ef3c00")
    ).not.toBeInTheDocument();
    expect(screen.getByText("Page 1 • 25 requests on page")).toBeInTheDocument();
  });

  it("handles combined filters: key selection, preset time range, and page size", async () => {
    let lastRequestedUrl = "";
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        lastRequestedUrl = url;
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-filter-bar")).toBeInTheDocument();
      expect(screen.getByRole("option", { name: /Production Bot Key/ })).toBeInTheDocument();
    });

    // 1. Select Key
    const keySelect = screen.getByLabelText("Filter by API key");
    fireEvent.change(keySelect, { target: { value: "key-prod-01" } });

    await waitFor(() => {
      expect(lastRequestedUrl).toContain("key_id=key-prod-01");
    });

    // 2. Change range preset to 24h
    const preset24h = screen.getByRole("button", { name: "24h" });
    fireEvent.click(preset24h);

    await waitFor(() => {
      expect(lastRequestedUrl).toContain("after=");
      expect(lastRequestedUrl).toContain("before=");
    });

    // 3. Change page size to 50
    const pageSizeSelect = screen.getByLabelText("Page size");
    fireEvent.change(pageSizeSelect, { target: { value: "50" } });

    await waitFor(() => {
      expect(lastRequestedUrl).toContain("limit=50");
    });

    // 4. Click Reset Filters
    const resetBtn = screen.getByRole("button", { name: "Reset all filters" });
    fireEvent.click(resetBtn);

    await waitFor(() => {
      expect(lastRequestedUrl).not.toContain("key_id=");
      expect(lastRequestedUrl).not.toContain("after=");
    });
  });

  it("validates Custom UTC RFC3339 ranges and handles invalid inputs", async () => {
    let lastRequestedUrl = "";
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        lastRequestedUrl = url;
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-filter-bar")).toBeInTheDocument();
    });

    // Click Custom UTC tab
    const customTab = screen.getByRole("button", { name: "Custom UTC" });
    fireEvent.click(customTab);

    expect(screen.getByTestId("custom-range-form")).toBeInTheDocument();

    const startInput = screen.getByLabelText("Start timestamp in UTC");
    const endInput = screen.getByLabelText("End timestamp in UTC");
    const applyBtn = screen.getByRole("button", { name: /apply range/i });

    // Error 1: Empty inputs
    fireEvent.click(applyBtn);
    expect(
      screen.getByText("Both Start UTC and End UTC timestamps are required.")
    ).toBeInTheDocument();

    // Error 2: Invalid start date format
    fireEvent.change(startInput, { target: { value: "not-a-date" } });
    fireEvent.change(endInput, { target: { value: "2026-09-18T00:00:00Z" } });
    fireEvent.click(applyBtn);
    expect(
      screen.getByText(/invalid start utc timestamp/i)
    ).toBeInTheDocument();

    // Error 3: Start >= End
    fireEvent.change(startInput, { target: { value: "2026-09-19T00:00:00Z" } });
    fireEvent.change(endInput, { target: { value: "2026-09-18T00:00:00Z" } });
    fireEvent.click(applyBtn);
    expect(
      screen.getByText(/start utc timestamp must be strictly before/i)
    ).toBeInTheDocument();

    // Success: Valid range
    fireEvent.change(startInput, { target: { value: "2026-09-17T00:00:00Z" } });
    fireEvent.change(endInput, { target: { value: "2026-09-18T00:00:00Z" } });
    fireEvent.click(applyBtn);

    await waitFor(() => {
      expect(lastRequestedUrl).toContain("after=2026-09-17T00%3A00%3A00.000Z");
      expect(lastRequestedUrl).toContain("before=2026-09-18T00%3A00%3A00.000Z");
    });
  });

  it.each([
    ["date-only start", "2026-09-17", "2026-09-18T00:00:00Z"],
    ["local-time start", "2026-09-17T00:00:00", "2026-09-18T00:00:00Z"],
    ["malformed end", "2026-09-17T00:00:00Z", "not-a-date"],
    ["reversed bounds", "2026-09-19T00:00:00Z", "2026-09-18T00:00:00Z"],
    ["leading whitespace start", " 2026-09-17T00:00:00Z", "2026-09-18T00:00:00Z"],
    ["trailing whitespace end", "2026-09-17T00:00:00Z", "2026-09-18T00:00:00Z "],
    ["whitespace-padded start and end", "  2026-09-17T00:00:00Z ", " 2026-09-18T00:00:00Z  "],
  ])("rejects %s bookmarked custom ranges without a partial list query", async (_label, after, before) => {
    const requestFetch = vi.fn();
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        requestFetch(url);
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage(`/requests?range=custom&after=${encodeURIComponent(after)}&before=${encodeURIComponent(before)}`);

    expect(await screen.findByTestId("requests-invalid-params-alert")).toBeInTheDocument();
    expect(requestFetch).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Reset Filters" })).toBeInTheDocument();
  });

  it("survives a valid copied link and restores filters", async () => {
    let lastRequestedUrl = "";
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        lastRequestedUrl = url;
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage("/requests?key_id=key-prod-01&range=24h&limit=50");

    await waitFor(() => {
      expect(lastRequestedUrl).toContain("key_id=key-prod-01");
      expect(lastRequestedUrl).toContain("limit=50");
      expect(lastRequestedUrl).toContain("after=");
    });

    const keySelect = screen.getByLabelText("Filter by API key") as HTMLSelectElement;
    expect(keySelect.value).toBe("key-prod-01");

    const pageSizeSelect = screen.getByLabelText("Page size") as HTMLSelectElement;
    expect(pageSizeSelect.value).toBe("50");
  });

  it("handles unknown or deleted key from a bookmarked URL in the selector", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: [] }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage("/requests?key_id=key-deleted-999");

    await waitFor(() => {
      expect(screen.getByTestId("requests-filter-bar")).toBeInTheDocument();
      expect(
        screen.getByRole("option", { name: "Unknown Key (key-deleted-999)" })
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("requests-empty-filtered-key")
      ).toBeInTheDocument();
    });

    expect(
      screen.getByText(/no completed requests found for api key "key-deleted-999"/i)
    ).toBeInTheDocument();
  });

  it("contextual empty state for time range filter with clear filters button", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: [] }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage("/requests?range=1h");

    await waitFor(() => {
      expect(screen.getByTestId("requests-empty-filtered-range")).toBeInTheDocument();
    });

    const clearBtn = screen.getByRole("button", { name: "Clear Filters" });
    fireEvent.click(clearBtn);

    await waitFor(() => {
      expect(screen.queryByTestId("requests-empty-filtered-range")).not.toBeInTheDocument();
    });
  });

  it("renders all terminal outcomes and ensures badges are not color-only", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    // Badges must contain textual status code and outcome (never color-only)
    expect(screen.getAllByText("200 Complete").length).toBeGreaterThan(0);
    expect(screen.getAllByText("400 Pre-Upstream Rejection").length).toBeGreaterThan(0);
    expect(screen.getAllByText("429 Rate Limited").length).toBeGreaterThan(0);
    expect(screen.getAllByText("502 Upstream Error").length).toBeGreaterThan(0);
  });

  it("strictly preserves null vs zero for tokens, cost, and duration", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    // The rate_limited item has explicit 0 for tokens and cost
    const rateLimitedRow = screen.getByTestId("request-row-req-outcome-rate-limited");
    expect(within(rateLimitedRow).getByText("0")).toBeInTheDocument();
    expect(within(rateLimitedRow).getByText("$0.00")).toBeInTheDocument();

    // The pre_upstream item has null tokens, cost, etc., which must be displayed as "—"
    const preUpstreamRow = screen.getByTestId("request-row-req-outcome-pre-upstream");
    const dashes = within(preUpstreamRow).getAllByText("—");
    expect(dashes.length).toBeGreaterThan(1);
  });

  it("provides an explicit copy-request-ID action with feedback", async () => {
    const writeTextMock = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, {
      clipboard: {
        writeText: writeTextMock,
      },
    });

    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    const copyBtn = screen.getByTestId("copy-id-req-outcome-complete");
    fireEvent.click(copyBtn);

    expect(writeTextMock).toHaveBeenCalledWith("req-outcome-complete");
    expect(
      screen.getByText("Request ID req-outcome-complete copied to clipboard.")
    ).toBeInTheDocument();
  });

  it("supports row deep links to request detail", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    const row = screen.getByTestId("request-row-req-outcome-complete");
    fireEvent.click(row);

    await waitFor(() => {
      expect(screen.getByTestId("request-detail-target")).toBeInTheDocument();
    });
  });

  it("handles 401 session expired error with sign-in action", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: [] }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(
          mockJsonResponse(
            { error: { message: "Unauthorized", code: "invalid_api_key" } },
            { status: 401 }
          )
        );
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-401-alert")).toBeInTheDocument();
    });

    expect(
      screen.getByText(/your administrative session has expired/i)
    ).toBeInTheDocument();
  });

  it("handles invalid or expired cursor error with return to first page action", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        if (url.includes("cursor=badcursor")) {
          return Promise.resolve(
            mockJsonResponse(
              { error: { message: "Invalid cursor", code: "cursor_expired" } },
              { status: 400 }
            )
          );
        }
        return Promise.resolve(
          mockJsonResponse({
            requests: mockPage1Requests,
            next_cursor: "badcursor",
          })
        );
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    // Go to next page which triggers bad cursor
    const nextBtn = screen.getByRole("button", { name: /go to next page/i });
    fireEvent.click(nextBtn);

    await waitFor(() => {
      expect(screen.getByTestId("requests-invalid-cursor-alert")).toBeInTheDocument();
    });

    // Click Return to First Page
    const returnBtn = screen.getByRole("button", { name: "Return to First Page" });
    fireEvent.click(returnBtn);

    await waitFor(() => {
      expect(
        screen.queryByTestId("requests-invalid-cursor-alert")
      ).not.toBeInTheDocument();
    });
  });

  it("handles offline status gracefully with alert banner", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    await act(async () => {
      onlineManager.setOnline(false);
      window.dispatchEvent(new Event("offline"));
    });

    await waitFor(() => {
      expect(screen.getByTestId("requests-offline-alert")).toBeInTheDocument();
    });
  });

  it("handles backend 500 error with retry button", async () => {
    let shouldFail = true;
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        if (shouldFail) {
          return Promise.resolve(
            mockJsonResponse(
              { error: { message: "Internal server error", code: "gateway_internal_error" } },
              { status: 500 }
            )
          );
        }
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-error-alert")).toBeInTheDocument();
    });

    shouldFail = false;
    const retryBtn = screen.getByRole("button", { name: "Retry" });
    fireEvent.click(retryBtn);

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });
  });

  it("strictly excludes unsupported search, model, status, and sort filters", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-table-view")).toBeInTheDocument();
    });

    // Ensure NO free-text search input
    expect(screen.queryByPlaceholderText(/search/i)).not.toBeInTheDocument();

    // Ensure NO status filter select
    expect(screen.queryByLabelText(/filter by status/i)).not.toBeInTheDocument();

    // Ensure NO model filter select
    expect(screen.queryByLabelText(/filter by model/i)).not.toBeInTheDocument();

    // Ensure NO raw queries, headers, bodies, or SQL are rendered
    const text = document.body.textContent || "";
    expect(text).not.toContain("SELECT ");
    expect(text).not.toContain("Bearer ");
  });

  it("keyboard navigation: Enter on mobile card navigates to request detail", async () => {
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/admin/v1/keys")) {
        return Promise.resolve(mockJsonResponse({ keys: mockKeys }));
      }
      if (url.includes("/admin/v1/requests")) {
        return Promise.resolve(mockJsonResponse({ requests: mockOutcomesRequests }));
      }
      return Promise.resolve(mockJsonResponse({}));
    });

    renderRequestsPage();

    await waitFor(() => {
      expect(screen.getByTestId("requests-cards-view")).toBeInTheDocument();
    });

    const card = screen.getByTestId("request-card-req-outcome-complete");
    fireEvent.keyDown(card, { key: "Enter" });

    await waitFor(() => {
      expect(screen.getByTestId("request-detail-target")).toBeInTheDocument();
    });
  });
});
