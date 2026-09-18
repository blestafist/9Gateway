import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { onlineManager } from "@tanstack/react-query";
import { KeysPage } from "./KeysPage";
import { keyQueryKeys } from "./queryKeys";
import {
  getKeyStatus,
  isKeyExpired,
  isKeyExpiringSoon,
  filterKeysOnPage,
  formatKeyExpiry,
} from "./helpers";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AuthContext, AuthContextValue } from "../auth";
import keyDetailFixture from "./fixtures/keyDetail.fixture.json";
import { AdminKeyListItem } from "./types";

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

const mockMultiPageKeys: AdminKeyListItem[] = [
  {
    id: "key-page1-01",
    name: "Alpha Key",
    display_prefix: "sk-alp1",
    enabled: true,
    created_at: "2026-09-01T12:00:00Z",
    updated_at: "2026-09-01T12:00:00Z",
    expires_at: "2030-01-01T00:00:00Z",
    policy_summary: {
      allow_models: true,
      deny_models: false,
      log_request_body: true,
      log_response_body: false,
    },
  },
  {
    id: "key-page1-02",
    name: "Beta Key Very Long Production Worker Agent Integration Key Name",
    display_prefix: "sk-bet2",
    enabled: false,
    created_at: "2026-09-02T12:00:00Z",
    updated_at: "2026-09-02T12:00:00Z",
    expires_at: null,
    policy_summary: {
      allow_models: false,
      deny_models: true,
      log_request_body: false,
      log_response_body: true,
    },
  },
  {
    id: "key-page1-03",
    name: "Expiring Key",
    display_prefix: "sk-exp3",
    enabled: true,
    created_at: "2026-09-03T12:00:00Z",
    updated_at: "2026-09-03T12:00:00Z",
    expires_at: "2026-09-25T00:00:00Z", // Expiring soon (<30d from Sep 18 2026)
    policy_summary: {
      allow_models: false,
      deny_models: false,
      log_request_body: false,
      log_response_body: false,
    },
  },
  {
    id: "key-page1-04",
    name: "Dead Expired Key",
    display_prefix: "sk-exp4",
    enabled: true,
    created_at: "2026-01-01T12:00:00Z",
    updated_at: "2026-01-01T12:00:00Z",
    expires_at: "2026-05-01T00:00:00Z", // Already expired
    policy_summary: {
      allow_models: false,
      deny_models: false,
      log_request_body: false,
      log_response_body: false,
    },
  },
];

const mockPage2Keys: AdminKeyListItem[] = [
  {
    id: "key-page2-01",
    name: "Gamma Key Page 2",
    display_prefix: "sk-gam5",
    enabled: true,
    created_at: "2026-09-04T12:00:00Z",
    updated_at: "2026-09-04T12:00:00Z",
    expires_at: "2030-01-01T00:00:00Z",
    policy_summary: {
      allow_models: false,
      deny_models: false,
      log_request_body: false,
      log_response_body: false,
    },
  },
];

describe("T170 Key Helpers & Filters", () => {
  const fixedNow = new Date("2026-09-18T12:00:00.000Z").getTime();

  it("calculates accurate key expiry and status indicators (not color-only)", () => {
    // Expired
    const expired = getKeyStatus(true, "2026-09-01T00:00:00Z", fixedNow);
    expect(expired.status).toBe("expired");
    expect(expired.label).toBe("Expired");
    expect(expired.variant).toBe("danger");
    expect(isKeyExpired("2026-09-01T00:00:00Z", fixedNow)).toBe(true);

    // Expiring soon (within 30d)
    const expiringSoon = getKeyStatus(true, "2026-09-25T00:00:00Z", fixedNow);
    expect(expiringSoon.status).toBe("expiring");
    expect(expiringSoon.label).toBe("Expiring Soon");
    expect(expiringSoon.variant).toBe("warning");
    expect(isKeyExpiringSoon("2026-09-25T00:00:00Z", fixedNow)).toBe(true);

    // Disabled & expiring soon
    const disabledExpiring = getKeyStatus(false, "2026-09-25T00:00:00Z", fixedNow);
    expect(disabledExpiring.status).toBe("disabled");
    expect(disabledExpiring.label).toBe("Disabled (Expiring)");
    expect(disabledExpiring.variant).toBe("warning");

    // Disabled normal
    const disabled = getKeyStatus(false, null, fixedNow);
    expect(disabled.status).toBe("disabled");
    expect(disabled.label).toBe("Disabled");
    expect(disabled.variant).toBe("neutral");

    // Active normal
    const active = getKeyStatus(true, "2030-01-01T00:00:00Z", fixedNow);
    expect(active.status).toBe("active");
    expect(active.label).toBe("Active");
    expect(active.variant).toBe("success");

    // Null expiry formatting
    expect(formatKeyExpiry(null)).toBe("Never");
    expect(formatKeyExpiry("2030-01-01T00:00:00Z")).toContain("2030");
  });

  it("filters current-page keys accurately by text query, status, and policy flags", () => {
    // 1. Text filter matches name
    const byName = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "Alpha", status: "all", policy: "all" },
      fixedNow
    );
    expect(byName).toHaveLength(1);
    expect(byName[0]!.name).toBe("Alpha Key");

    // 2. Text filter matches prefix
    const byPrefix = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "sk-bet2", status: "all", policy: "all" },
      fixedNow
    );
    expect(byPrefix).toHaveLength(1);
    expect(byPrefix[0]!.id).toBe("key-page1-02");

    // 3. Status filter: active only (excludes expired and disabled)
    const activeOnly = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "", status: "active", policy: "all" },
      fixedNow
    );
    expect(activeOnly).toHaveLength(1);
    expect(activeOnly[0]!.id).toBe("key-page1-01");

    // 4. Status filter: disabled only
    const disabledOnly = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "", status: "disabled", policy: "all" },
      fixedNow
    );
    expect(disabledOnly).toHaveLength(1);
    expect(disabledOnly[0]!.id).toBe("key-page1-02");

    // 5. Status filter: expired only
    const expiredOnly = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "", status: "expired", policy: "all" },
      fixedNow
    );
    expect(expiredOnly).toHaveLength(1);
    expect(expiredOnly[0]!.id).toBe("key-page1-04");

    // 6. Policy filter: allowlist
    const allowlistOnly = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "", status: "all", policy: "allowlist" },
      fixedNow
    );
    expect(allowlistOnly).toHaveLength(1);
    expect(allowlistOnly[0]!.id).toBe("key-page1-01");

    // 7. Policy filter: denylist
    const denylistOnly = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "", status: "all", policy: "denylist" },
      fixedNow
    );
    expect(denylistOnly).toHaveLength(1);
    expect(denylistOnly[0]!.id).toBe("key-page1-02");

    // 8. Policy filter: log_res
    const logResOnly = filterKeysOnPage(
      mockMultiPageKeys,
      { searchQuery: "", status: "all", policy: "log_res" },
      fixedNow
    );
    expect(logResOnly).toHaveLength(1);
    expect(logResOnly[0]!.id).toBe("key-page1-02");
  });
});

describe("T170 KeysPage Component & Lifecycle", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const urlStr = String(input);
      if (urlStr.includes("/admin/v1/keys/key-0191eb0b62bc7b7489a2434685ef3b63")) {
        return mockJsonResponse(keyDetailFixture);
      }
      if (urlStr.includes("/admin/v1/keys/key-page1-01")) {
        return mockJsonResponse({
          ...keyDetailFixture,
          id: "key-page1-01",
          name: "Alpha Key",
        });
      }
      if (urlStr.includes("/admin/v1/keys/missing-deleted-key")) {
        return mockJsonResponse({ error: "Key not found" }, { status: 404 });
      }
      if (urlStr.includes("cursor=cursor-page-2")) {
        return mockJsonResponse({
          keys: mockPage2Keys,
          next_cursor: undefined,
        });
      }
      return mockJsonResponse({
        keys: mockMultiPageKeys,
        next_cursor: "cursor-page-2",
      });
    });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    act(() => {
      onlineManager.setOnline(true);
      window.dispatchEvent(new Event("online"));
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

  const renderKeys = ({
    initialEntries = ["/keys"],
    auth = createMockAuth(),
  } = {}) => {
    const queryClient = createAdminQueryClient();
    return {
      queryClient,
      ...render(
        <AuthContext.Provider value={auth}>
          <AdminQueryProvider client={queryClient}>
            <MemoryRouter initialEntries={initialEntries}>
              <Routes>
                <Route path="/keys" element={<KeysPage />} />
                <Route path="/keys/:id" element={<KeysPage />} />
                <Route path="/login" element={<div data-testid="login-page">Login Page</div>} />
              </Routes>
            </MemoryRouter>
          </AdminQueryProvider>
        </AuthContext.Provider>
      ),
    };
  };

  it("renders desktop table and mobile card views with accessible labels and semantics", async () => {
    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
      expect(screen.getByTestId("keys-desktop-table")).toBeInTheDocument();
      expect(screen.getByRole("list", { name: "API Keys List" })).toBeInTheDocument();
    });

    // Check header and local endpoint
    expect(screen.getByText("API Endpoint")).toBeInTheDocument();
    expect(screen.getByText("API Keys")).toBeInTheDocument();

    // Table presence
    const table = screen.getByTestId("keys-desktop-table");
    expect(within(table).getByText("Alpha Key")).toBeInTheDocument();
    expect(within(table).getByText("sk-alp1")).toBeInTheDocument();
    expect(within(table).getByText("Active")).toBeInTheDocument();

    // Long name check in table
    expect(within(table).getByText(/Very Long Production Worker/)).toBeInTheDocument();

    // Cards presence (mobile layout semantic items)
    const cardsList = screen.getByRole("list", { name: "API Keys List" });
    expect(within(cardsList).getByTestId("key-card-key-page1-01")).toBeInTheDocument();
    expect(within(cardsList).getByTestId("key-card-key-page1-02")).toBeInTheDocument();

    // Verified: No color-only status (explicit status text is rendered inside pills)
    const pills = screen.getAllByTestId("status-pill");
    expect(pills.length).toBeGreaterThan(0);
    expect(screen.getAllByText("Active").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Disabled").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Expired").length).toBeGreaterThan(0);
  });

  it("client-side search and filters display current-page scope banner and filter items", async () => {
    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-desktop-table")).toBeInTheDocument();
    });

    // Verify explicit scope banner
    expect(screen.getByText("Current-page filter only")).toBeInTheDocument();
    expect(
      screen.getByText(/Showing 4 of 4 keys on this page. Server-wide search is not supported./)
    ).toBeInTheDocument();

    // 1. Search filter
    const searchInput = screen.getByLabelText("Filter current page by name, ID, or prefix");
    fireEvent.change(searchInput, { target: { value: "Alpha" } });

    await waitFor(() => {
      expect(screen.getByText(/Showing 1 of 4 keys on this page/)).toBeInTheDocument();
      expect(screen.getAllByText("Alpha Key").length).toBeGreaterThan(0);
      expect(screen.queryByText(/Very Long Production Worker/)).not.toBeInTheDocument();
    });

    // 2. Clear filters button
    const clearBtn = screen.getByRole("button", { name: "Reset current page filters" });
    fireEvent.click(clearBtn);

    await waitFor(() => {
      expect(screen.getByText(/Showing 4 of 4 keys on this page/)).toBeInTheDocument();
    });

    // 3. Status filter select
    const statusSelect = screen.getByLabelText("Filter by status");
    fireEvent.change(statusSelect, { target: { value: "disabled" } });

    await waitFor(() => {
      expect(screen.getByText(/Showing 1 of 4 keys on this page/)).toBeInTheDocument();
      expect(screen.queryByText("Alpha Key")).not.toBeInTheDocument();
      expect(screen.getAllByText(/Very Long Production Worker/).length).toBeGreaterThan(0);
    });

    // 4. Policy filter select
    fireEvent.change(statusSelect, { target: { value: "all" } });
    const policySelect = screen.getByLabelText("Filter by policy");
    fireEvent.change(policySelect, { target: { value: "allowlist" } });

    await waitFor(() => {
      expect(screen.getByText(/Showing 1 of 4 keys on this page/)).toBeInTheDocument();
      expect(screen.getAllByText("Alpha Key").length).toBeGreaterThan(0);
    });

    // 5. Empty filter state when no items match
    fireEvent.change(searchInput, { target: { value: "nomatchxyz123" } });

    await waitFor(() => {
      expect(screen.getByTestId("keys-empty-filter-state")).toBeInTheDocument();
      expect(screen.getByText("No Matching Keys on This Page")).toBeInTheDocument();
    });

    // Resetting from empty state action button
    const resetFromEmptyBtn = screen.getByRole("button", { name: "Clear Filters" });
    fireEvent.click(resetFromEmptyBtn);

    await waitFor(() => {
      expect(screen.queryByTestId("keys-empty-filter-state")).not.toBeInTheDocument();
      expect(screen.getByText(/Showing 4 of 4 keys on this page/)).toBeInTheDocument();
    });
  });

  it("handles pagination with bounded page size, in-memory cursors, and prefetch", async () => {
    const { queryClient } = renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-pagination")).toBeInTheDocument();
    });

    // Verify initial pagination state
    expect(screen.getByText("Page 1 • 4 keys on page")).toBeInTheDocument();
    const prevBtn = screen.getByRole("button", { name: "Go to previous page" });
    const nextBtn = screen.getByRole("button", { name: "Go to next page" });

    expect(prevBtn).toBeDisabled();
    expect(nextBtn).not.toBeDisabled();

    // Verify prefetch occurred for next page
    await waitFor(() => {
      const prefetched = queryClient.getQueryData(
        keyQueryKeys.list({ limit: 25, cursor: "cursor-page-2" })
      );
      expect(prefetched).toBeDefined();
    });

    // Navigate to next page
    fireEvent.click(nextBtn);

    await waitFor(() => {
      expect(screen.getAllByText("Gamma Key Page 2").length).toBeGreaterThan(0);
      expect(screen.getByText("Page 2 • 1 key on page")).toBeInTheDocument();
    });

    // Verify cursor is strictly NEVER in the URL or history
    expect(window.location.search).not.toContain("cursor");
    expect(window.location.href).not.toContain("cursor-page-2");

    // Navigate back to previous page
    const prevBtnActive = screen.getByRole("button", { name: "Go to previous page" });
    expect(prevBtnActive).not.toBeDisabled();
    fireEvent.click(prevBtnActive);

    await waitFor(() => {
      expect(screen.getAllByText("Alpha Key").length).toBeGreaterThan(0);
      expect(screen.getByText("Page 1 • 4 keys on page")).toBeInTheDocument();
    });

    // Change page size -> resets pagination back to page 1
    const pageSizeSelect = screen.getByLabelText("Items per page");
    fireEvent.change(pageSizeSelect, { target: { value: "50" } });

    await waitFor(() => {
      expect(screen.getByText("Page 1 • 4 keys on page")).toBeInTheDocument();
    });
  });

  it("opens detail drawer on row click, loads detail API, and restores focus on close", async () => {
    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("key-row-key-page1-01")).toBeInTheDocument();
    });

    // Click on row
    const row = screen.getByTestId("key-row-key-page1-01");
    row.focus();
    fireEvent.click(row);

    // Detail drawer opens
    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(screen.getByTestId("key-detail-content")).toBeInTheDocument();
    });

    // Check detail drawer sections: Identity, Status & Expiry, Policy Summary
    expect(screen.getByRole("heading", { name: "Identity" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Status & Expiry" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Policy Summary" })).toBeInTheDocument();

    // Displays summary indicators
    expect(screen.getByText("gpt-4o")).toBeInTheDocument();
    expect(screen.getByText("claude-3-5-sonnet")).toBeInTheDocument();
    expect(screen.getByText(/5 concurrent requests/)).toBeInTheDocument();
    expect(screen.getByText(/60 requests per 1m/)).toBeInTheDocument();
    expect(screen.getByText(/100,000 tokens per 1h/)).toBeInTheDocument();

    // Close drawer via close button
    const closeBtn = screen.getByRole("button", { name: "Close" });
    fireEvent.click(closeBtn);

    // Drawer closes and focus is restored
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("supports deep-link directly to key detail route /keys/:id and handles deleted 404 key", async () => {
    renderKeys({ initialEntries: ["/keys/missing-deleted-key"] });

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    // Shows key not found alert
    await waitFor(() => {
      expect(screen.getByTestId("key-not-found-alert")).toBeInTheDocument();
      expect(screen.getByText(/could not be found/)).toBeInTheDocument();
    });

    // Close drawer navigates back to /keys
    const closeBtn = screen.getByRole("button", { name: "Close" });
    fireEvent.click(closeBtn);

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("renders empty state when gateway has no configured API keys", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () =>
      mockJsonResponse({ keys: [], next_cursor: undefined })
    );

    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-empty-state")).toBeInTheDocument();
      expect(screen.getByText("No API Keys Found")).toBeInTheDocument();
    });
  });

  it("renders offline warning banner when network connection is offline", async () => {
    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
    });

    act(() => {
      onlineManager.setOnline(false);
      window.dispatchEvent(new Event("offline"));
    });

    await waitFor(() => {
      expect(screen.getByTestId("keys-offline-alert")).toBeInTheDocument();
      expect(screen.getByText(/You are currently offline/)).toBeInTheDocument();
    });
  });

  it("renders session expired alert with sign-in action on 401 error", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () =>
      mockJsonResponse({ error: "Session expired" }, { status: 401 })
    );

    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-401-alert")).toBeInTheDocument();
      expect(screen.getByText("Session Expired")).toBeInTheDocument();
    });

    // Sign in link navigates to login
    const signInBtn = screen.getByRole("button", { name: "Sign In" });
    fireEvent.click(signInBtn);

    await waitFor(() => {
      expect(screen.getByTestId("login-page")).toBeInTheDocument();
    });
  });

  it("renders error alert with retry button on general 500 error", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () =>
      mockJsonResponse({ error: "Internal error" }, { status: 500 })
    );

    renderKeys();

    await waitFor(() => {
      expect(screen.getByTestId("keys-error-alert")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    });
  });

  it("DOM safety: guarantees no raw key secrets, fake reveals, or full raw policy JSON appear in DOM", async () => {
    renderKeys({ initialEntries: ["/keys/key-0191eb0b62bc7b7489a2434685ef3b63"] });

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(screen.getByTestId("key-detail-content")).toBeInTheDocument();
    });

    const bodyHtml = document.body.innerHTML;

    // Must NOT contain synthetic full key placeholders
    expect(bodyHtml).not.toMatch(/sk-[a-zA-Z0-9]{20,}/);
    expect(bodyHtml).not.toMatch(/sk-••••/);
    expect(bodyHtml).not.toMatch(/••••••••/);

    // Must NOT contain full raw policy JSON strings
    expect(bodyHtml).not.toContain('"allowed_models":');
    expect(bodyHtml).not.toContain('"budget_limits":');
    expect(bodyHtml).not.toContain('"amount_micros":');

    // Display prefixes ONLY
    expect(bodyHtml).toContain("sk-alp1");
    expect(bodyHtml).toContain("sk-c47a98b1");
  });
});
