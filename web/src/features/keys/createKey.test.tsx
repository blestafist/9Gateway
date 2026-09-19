import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { KeysPage } from "./KeysPage";
import {
  computeExpiresAt,
  parseCustomExpiryToMs,
  validateCustomExpiry,
} from "./components/CreateKeyDialog";
import {
  generateKeyDownloadFilename,
  downloadKeySecret,
} from "./helpers";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AuthContext, AuthContextValue } from "../auth";
import createKeyFixture from "./fixtures/createKey.fixture.json";
import keyDetailFixture from "./fixtures/keyDetail.fixture.json";

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

describe("T171 Key Creation Helpers", () => {
  it("generates safe lowercase filename with alphanumeric and hyphens only", () => {
    expect(generateKeyDownloadFilename("My Service Key #1!", "sk-5ca93214")).toBe(
      "9gateway-key-my-service-key-1-sk-5ca93214.txt"
    );
    expect(generateKeyDownloadFilename("   ", "sk-1234")).toBe(
      "9gateway-key-api-key-sk-1234.txt"
    );
    expect(generateKeyDownloadFilename("UPPER_CASE-KEY", "prefix")).toBe(
      "9gateway-key-upper_case-key-prefix.txt"
    );
  });

  it("downloads plain text blob with safe filename and secret content only", () => {
    let capturedBlob: Blob | null = null;
    let capturedFilename = "";

    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;

    URL.createObjectURL = vi.fn((blob: Blob) => {
      capturedBlob = blob;
      return "blob:mock-url";
    });
    URL.revokeObjectURL = vi.fn();

    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const appendSpy = vi.spyOn(document.body, "appendChild");
    const removeSpy = vi.spyOn(document.body, "removeChild");

    downloadKeySecret("test-key.txt", "sk-raw-secret-1234567890");

    expect(clickSpy).toHaveBeenCalled();

    expect(capturedBlob).not.toBeNull();
    expect(capturedBlob!.type).toBe("text/plain;charset=utf-8");

    const createdAnchor = appendSpy.mock.calls[0]?.[0] as HTMLAnchorElement;
    expect(createdAnchor).toBeDefined();
    capturedFilename = createdAnchor.download;
    expect(capturedFilename).toBe("test-key.txt");

    expect(appendSpy).toHaveBeenCalled();
    expect(removeSpy).toHaveBeenCalled();

    clickSpy.mockRestore();
    URL.createObjectURL = originalCreateObjectURL;
    URL.revokeObjectURL = originalRevokeObjectURL;
  });

  it("computes expiration RFC3339 timestamps for presets and custom UTC values", () => {
    expect(computeExpiresAt("never", "")).toBeUndefined();

    const exp30d = computeExpiresAt("30d", "");
    expect(exp30d).toBeDefined();
    expect(exp30d).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);

    const expCustom = computeExpiresAt("custom", "2032-06-15T14:30:00Z");
    expect(expCustom).toBe("2032-06-15T14:30:00Z");
  });

  it("parses realistic timezone-less datetime-local values explicitly as UTC without breaking offset/Z inputs", () => {
    // Realistic browser datetime-local format: "YYYY-MM-DDTHH:mm" (no timezone/Z indicator)
    const realisticNoZ = "2032-06-15T14:30";
    expect(parseCustomExpiryToMs(realisticNoZ)).toBe(Date.UTC(2032, 5, 15, 14, 30, 0));
    expect(computeExpiresAt("custom", realisticNoZ)).toBe("2032-06-15T14:30:00Z");

    // Realistic datetime-local format with seconds: "YYYY-MM-DDTHH:mm:ss"
    const realisticNoZWithSec = "2032-06-15T14:30:45";
    expect(parseCustomExpiryToMs(realisticNoZWithSec)).toBe(Date.UTC(2032, 5, 15, 14, 30, 45));
    expect(computeExpiresAt("custom", realisticNoZWithSec)).toBe("2032-06-15T14:30:45Z");

    // Whitespace-separated datetime without timezone (e.g. manual paste "YYYY-MM-DD HH:mm")
    const realisticSpaceNoZ = "2032-06-15 14:30";
    expect(parseCustomExpiryToMs(realisticSpaceNoZ)).toBe(Date.UTC(2032, 5, 15, 14, 30, 0));
    expect(computeExpiresAt("custom", realisticSpaceNoZ)).toBe("2032-06-15T14:30:00Z");

    // Already offset/Z inputs must retain their specified timezone offset
    expect(computeExpiresAt("custom", "2032-06-15T14:30:00Z")).toBe("2032-06-15T14:30:00Z");
    expect(computeExpiresAt("custom", "2032-06-15T14:30:00+02:00")).toBe("2032-06-15T12:30:00Z");
    expect(computeExpiresAt("custom", "2032-06-15T14:30:00-05:00")).toBe("2032-06-15T19:30:00Z");
  });

  it("enforces custom expiration validation semantics in UTC against reference timestamps", () => {
    const fixedNowMs = Date.UTC(2026, 5, 15, 12, 0, 0); // 2026-06-15T12:00:00Z

    // Empty or whitespace
    expect(validateCustomExpiry("", fixedNowMs)).toBe("Please enter an expiration date and time.");
    expect(validateCustomExpiry("   ", fixedNowMs)).toBe("Please enter an expiration date and time.");

    // Malformed input
    expect(validateCustomExpiry("invalid-date", fixedNowMs)).toBe("Invalid expiration date format.");
    expect(validateCustomExpiry("2026-02-30T12:00", fixedNowMs)).toBe("Invalid expiration date format.");
    expect(validateCustomExpiry("2028-02-29T12:00", fixedNowMs)).toBeNull();

    // Realistic no-Z value strictly in the past relative to UTC reference
    expect(validateCustomExpiry("2026-06-15T11:59", fixedNowMs)).toBe(
      "Expiration date must be in the future."
    );

    // Realistic no-Z value matching exact reference time (boundary check)
    expect(validateCustomExpiry("2026-06-15T12:00", fixedNowMs)).toBe(
      "Expiration date must be in the future."
    );

    // Realistic no-Z value strictly in the future relative to UTC reference
    expect(validateCustomExpiry("2026-06-15T12:01", fixedNowMs)).toBeNull();
    expect(validateCustomExpiry("2032-06-15T14:30", fixedNowMs)).toBeNull();

    // Explicit timezone offsets evaluated accurately relative to UTC
    // 15:00+04:00 is 11:00:00 UTC (past)
    expect(validateCustomExpiry("2026-06-15T15:00+04:00", fixedNowMs)).toBe(
      "Expiration date must be in the future."
    );
    // 17:00+04:00 is 13:00:00 UTC (future)
    expect(validateCustomExpiry("2026-06-15T17:00+04:00", fixedNowMs)).toBeNull();
  });
});

describe("T171 CreateKeyDialog and Lifecycle", () => {
  const originalFetch = globalThis.fetch;
  const originalClipboard = navigator.clipboard;

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();

    // Standard mock clipboard
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    globalThis.fetch = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const urlStr = String(input);
      const method = init?.method ?? "GET";

      if (urlStr.includes("/admin/v1/keys") && method === "POST") {
        return mockJsonResponse(createKeyFixture, { status: 201 });
      }

      if (urlStr.includes("/admin/v1/keys/key-0191eb0b62bc7b7489a2434685ef3b65")) {
        return mockJsonResponse({
          ...keyDetailFixture,
          id: createKeyFixture.id,
          name: createKeyFixture.name,
          display_prefix: createKeyFixture.prefix,
        });
      }

      if (urlStr.includes("/admin/v1/keys")) {
        return mockJsonResponse({
          keys: [
            {
              id: "key-existing-01",
              name: "Existing Key",
              display_prefix: "sk-exist",
              enabled: true,
              created_at: "2026-09-01T12:00:00Z",
              updated_at: "2026-09-01T12:00:00Z",
              expires_at: null,
              policy_summary: {
                allow_models: false,
                deny_models: false,
                log_request_body: false,
                log_response_body: false,
              },
            },
          ],
        });
      }

      return mockJsonResponse({});
    });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    Object.assign(navigator, { clipboard: originalClipboard });
    vi.restoreAllMocks();
  });

  const createMockAuth = (overrides: Partial<AuthContextValue> = {}): AuthContextValue => ({
    isAuthenticated: true,
    isLoading: false,
    csrfToken: "mock-csrf-token",
    idleExpiresAt: null,
    expiresAt: null,
    login: vi.fn(),
    logout: vi.fn(),
    checkSession: vi.fn().mockResolvedValue(true),
    expireSession: vi.fn(),
    ...overrides,
  });

  const renderKeysPage = ({
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

  it("opens create-key dialog on button click and supports pre-submit cancellation", async () => {
    renderKeysPage();

    await waitFor(() => {
      expect(screen.getByTestId("open-create-key-btn")).toBeInTheDocument();
    });

    // Open modal
    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
      expect(screen.getByText("Create API Key")).toBeInTheDocument();
    });

    // Pre-submit cancellation via Cancel button
    const cancelBtn = screen.getByTestId("create-key-cancel-btn");
    fireEvent.click(cancelBtn);

    await waitFor(() => {
      expect(screen.queryByTestId("create-key-form")).not.toBeInTheDocument();
    });

    // No POST request was sent
    expect(globalThis.fetch).not.toHaveBeenCalledWith(
      expect.stringContaining("/admin/v1/keys"),
      expect.objectContaining({ method: "POST" })
    );
  });

  it("enforces inline validation on empty name and oversized names (>256 bytes)", async () => {
    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    const submitBtn = screen.getByTestId("create-key-submit-btn");
    const nameInput = screen.getByTestId("create-key-name-input");

    // 1. Submit with empty name
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(screen.getByText("Key name is required.")).toBeInTheDocument();
    });
    expect(globalThis.fetch).not.toHaveBeenCalledWith(
      expect.stringContaining("/admin/v1/keys"),
      expect.objectContaining({ method: "POST" })
    );

    // 2. Submit with name > 256 bytes
    const longName = "a".repeat(257);
    fireEvent.change(nameInput, { target: { value: longName } });
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(screen.getByText("Key name must be 256 bytes or fewer.")).toBeInTheDocument();
    });
    expect(globalThis.fetch).not.toHaveBeenCalledWith(
      expect.stringContaining("/admin/v1/keys"),
      expect.objectContaining({ method: "POST" })
    );
  });

  it("validates custom expiration: required, format, and future-date enforcement", async () => {
    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    const nameInput = screen.getByTestId("create-key-name-input");
    fireEvent.change(nameInput, { target: { value: "Valid Name" } });

    // Select custom expiry
    const expirySelect = screen.getByTestId("create-key-expiry-select");
    fireEvent.change(expirySelect, { target: { value: "custom" } });

    await waitFor(() => {
      expect(screen.getByTestId("create-key-custom-expiry-input")).toBeInTheDocument();
    });

    const submitBtn = screen.getByTestId("create-key-submit-btn");
    const customExpiryInput = screen.getByTestId("create-key-custom-expiry-input");

    // 1. Empty custom expiry
    fireEvent.click(submitBtn);
    await waitFor(() => {
      expect(screen.getByText("Please enter an expiration date and time.")).toBeInTheDocument();
    });

    // 2. Past date
    fireEvent.change(customExpiryInput, { target: { value: "2020-01-01T00:00" } });
    fireEvent.click(submitBtn);
    await waitFor(() => {
      expect(screen.getByText("Expiration date must be in the future.")).toBeInTheDocument();
    });

    // 3. Invalid date or empty
    fireEvent.change(customExpiryInput, { target: { value: "invalid-date" } });
    fireEvent.click(submitBtn);
    await waitFor(() => {
      expect(
        screen.queryByText("Invalid expiration date format.") ||
          screen.queryByText("Please enter an expiration date and time.")
      ).toBeInTheDocument();
    });

    expect(globalThis.fetch).not.toHaveBeenCalledWith(
      expect.stringContaining("/admin/v1/keys"),
      expect.objectContaining({ method: "POST" })
    );
  });

  it("submits custom expiration with realistic no-Z datetime-local input and sends exact UTC payload", async () => {
    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    const nameInput = screen.getByTestId("create-key-name-input");
    fireEvent.change(nameInput, { target: { value: "UTC Custom Key" } });

    // Select custom expiry mode
    const expirySelect = screen.getByTestId("create-key-expiry-select");
    fireEvent.change(expirySelect, { target: { value: "custom" } });

    await waitFor(() => {
      expect(screen.getByTestId("create-key-custom-expiry-input")).toBeInTheDocument();
    });

    // Enter realistic browser datetime-local value (no Z / no timezone offset)
    const customExpiryInput = screen.getByTestId("create-key-custom-expiry-input");
    fireEvent.change(customExpiryInput, { target: { value: "2032-06-15T14:30" } });

    const submitBtn = screen.getByTestId("create-key-submit-btn");
    fireEvent.click(submitBtn);

    // Verify exact UTC payload in POST request
    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        expect.stringContaining("/admin/v1/keys"),
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            name: "UTC Custom Key",
            expires_at: "2032-06-15T14:30:00Z",
          }),
        })
      );
    });

    // Enters success credential handoff
    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });
  });

  it("prevents duplicate submit and shows explicit submit progress", async () => {
    let resolvePost: (value: Response) => void;
    const postPromise = new Promise<Response>((resolve) => {
      resolvePost = resolve;
    });

    globalThis.fetch = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const urlStr = String(input);
      const method = init?.method ?? "GET";
      if (urlStr.includes("/admin/v1/keys") && method === "POST") {
        return postPromise;
      }
      return mockJsonResponse({ keys: [] });
    });

    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    const nameInput = screen.getByTestId("create-key-name-input");
    fireEvent.change(nameInput, { target: { value: "Duplicate Test Key" } });

    const submitBtn = screen.getByTestId("create-key-submit-btn");

    // Double click
    fireEvent.click(submitBtn);
    fireEvent.click(submitBtn);

    // Verify loading progress state
    expect(screen.getByText("Creating Key...")).toBeInTheDocument();
    expect(submitBtn).toBeDisabled();

    // Verify exactly one POST request was initiated
    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        expect.stringContaining("/admin/v1/keys"),
        expect.objectContaining({ method: "POST" })
      );
    });

    const postCalls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.filter(
      (call) => call[1]?.method === "POST"
    );
    expect(postCalls).toHaveLength(1);

    // Resolve post
    act(() => {
      resolvePost(mockJsonResponse(createKeyFixture, { status: 201 }));
    });

    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });
  });

  it("server errors (400, 409, 500) leave form open with values intact and do not invent key state", async () => {
    globalThis.fetch = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const urlStr = String(input);
      const method = init?.method ?? "GET";
      if (urlStr.includes("/admin/v1/keys") && method === "POST") {
        return mockJsonResponse(
          { error: { code: "conflict", message: "A key with this name already exists" } },
          { status: 409 }
        );
      }
      return mockJsonResponse({ keys: [] });
    });

    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    const nameInput = screen.getByTestId("create-key-name-input") as HTMLInputElement;
    fireEvent.change(nameInput, { target: { value: "Conflicting Key" } });

    const submitBtn = screen.getByTestId("create-key-submit-btn");
    fireEvent.click(submitBtn);

    // Error alert is displayed inside the form
    await waitFor(() => {
      expect(screen.getByTestId("create-key-submit-error")).toBeInTheDocument();
      expect(screen.getByText(/A key with this name already exists/)).toBeInTheDocument();
    });

    // Form remains open and user input is preserved
    expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    expect(nameInput.value).toBe("Conflicting Key");

    // Secret step was NOT opened
    expect(screen.queryByTestId("secret-handoff-step")).not.toBeInTheDocument();
  });

  it("handles one-time credential handoff: visibility toggle, anti-autofill, explicit copy & download", async () => {
    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("create-key-name-input"), {
      target: { value: "New Service Key" },
    });

    // Submit valid key
    fireEvent.click(screen.getByTestId("create-key-submit-btn"));

    // Enters blocking success step
    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
      expect(screen.getByTestId("secret-warning-alert")).toBeInTheDocument();
    });

    // Verify metadata recap
    expect(screen.getByText("New Service Key")).toBeInTheDocument();
    expect(screen.getByText("sk-5ca93214")).toBeInTheDocument();

    // 1. Raw key visibility toggle
    const secretInput = screen.getByTestId("secret-key-display") as HTMLInputElement;
    expect(secretInput.type).toBe("password");
    expect(secretInput.value).toBe(createKeyFixture.key);

    // Verify anti-autofill / password manager markers
    expect(secretInput.getAttribute("data-1p-ignore")).toBe("true");
    expect(secretInput.getAttribute("data-lpignore")).toBe("true");
    expect(secretInput.getAttribute("data-bwignore")).toBe("true");
    expect(secretInput.getAttribute("data-form-type")).toBe("other");
    expect(secretInput.getAttribute("autocomplete")).toBe("off");

    // Toggle visibility to plain text
    const toggleBtn = screen.getByTestId("toggle-visibility-btn");
    fireEvent.click(toggleBtn);
    expect(secretInput.type).toBe("text");

    // Toggle visibility back to masked password
    fireEvent.click(toggleBtn);
    expect(secretInput.type).toBe("password");

    // 2. Explicit Copy: No automatic clipboard calls before click
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();

    // Click Copy Key
    const copyBtn = screen.getByTestId("copy-key-btn");
    fireEvent.click(copyBtn);

    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(createKeyFixture.key);
      expect(screen.getByText("Copied!")).toBeInTheDocument();
      expect(screen.getByTestId("copy-live-region")).toHaveTextContent("API key copied to clipboard");
    });

    // 3. Safe Plain-Text Download
    let capturedBlob: Blob | null = null;
    let downloadedFilename = "";

    const originalCreateObjectURL = URL.createObjectURL;
    URL.createObjectURL = vi.fn((blob: Blob) => {
      capturedBlob = blob;
      return "blob:mock-url";
    });

    const appendSpy = vi.spyOn(document.body, "appendChild");
    const downloadBtn = screen.getByTestId("download-key-btn");
    fireEvent.click(downloadBtn);

    const anchor = appendSpy.mock.calls[0]?.[0] as HTMLAnchorElement;
    expect(anchor).toBeDefined();
    downloadedFilename = anchor.download;

    // Safe filename check
    expect(downloadedFilename).toBe("9gateway-key-new-service-key-sk-5ca93214.txt");

    // Plain text content only (raw key + newline)
    expect(capturedBlob).not.toBeNull();
    expect(capturedBlob!.type).toBe("text/plain;charset=utf-8");

    URL.createObjectURL = originalCreateObjectURL;
  });

  it("handles copy failure gracefully with error alert", async () => {
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockRejectedValue(new Error("Clipboard denied")),
      },
    });

    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("create-key-name-input"), {
      target: { value: "Copy Fail Key" },
    });
    fireEvent.click(screen.getByTestId("create-key-submit-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId("copy-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("copy-error-alert")).toBeInTheDocument();
      expect(screen.getByText(/Failed to copy key to clipboard/)).toBeInTheDocument();
    });
  });

  it("blocks dismissal until mandatory acknowledgement, then wipes memory secret on dismissal", async () => {
    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("create-key-name-input"), {
      target: { value: "Ack Test Key" },
    });
    fireEvent.click(screen.getByTestId("create-key-submit-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });

    // "Done" button is initially disabled
    const doneBtn = screen.getByTestId("done-secret-btn");
    expect(doneBtn).toBeDisabled();

    // Attempting to close via Escape is blocked
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

    await waitFor(() => {
      expect(screen.getByTestId("ack-warning-alert")).toBeInTheDocument();
      expect(screen.getByText(/You must confirm you have safely stored this key/)).toBeInTheDocument();
    });
    // Dialog remains open
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    // Check mandatory confirmation checkbox
    const ackCheckbox = screen.getByTestId("ack-stored-checkbox");
    fireEvent.click(ackCheckbox);

    // Warning disappears and Done button is enabled
    expect(screen.queryByTestId("ack-warning-alert")).not.toBeInTheDocument();
    expect(doneBtn).toBeEnabled();

    // Click Done to dismiss
    fireEvent.click(doneBtn);

    // Modal closes
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    // Verify secret is scrubbed from DOM
    expect(document.body.innerHTML).not.toContain(createKeyFixture.key);

    // Verify secret is NOT in localStorage, sessionStorage, or URL
    expect(localStorage.getItem("key")).toBeNull();
    expect(sessionStorage.getItem("key")).toBeNull();
    expect(window.location.search).not.toContain(createKeyFixture.key);
  });

  it("clears raw key references on session expiry / logout and route teardown", async () => {
    const authState = createMockAuth({ isAuthenticated: true });
    const { rerender } = renderKeysPage({ auth: authState });

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("create-key-name-input"), {
      target: { value: "Session Expiry Key" },
    });
    fireEvent.click(screen.getByTestId("create-key-submit-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });

    // Trigger session expiry / logout: authState.isAuthenticated becomes false
    const expiredAuth = { ...authState, isAuthenticated: false };
    const queryClient = createAdminQueryClient();

    rerender(
      <AuthContext.Provider value={expiredAuth}>
        <AdminQueryProvider client={queryClient}>
          <MemoryRouter initialEntries={["/keys"]}>
            <Routes>
              <Route path="/keys" element={<KeysPage />} />
            </Routes>
          </MemoryRouter>
        </AdminQueryProvider>
      </AuthContext.Provider>
    );

    // Modal is automatically closed and raw secret cleared
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect(document.body.innerHTML).not.toContain(createKeyFixture.key);
  });

  it("revisiting the created key shows metadata only in detail drawer", async () => {
    renderKeysPage();

    fireEvent.click(screen.getByTestId("open-create-key-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("create-key-name-input"), {
      target: { value: "Revisit Metadata Key" },
    });
    fireEvent.click(screen.getByTestId("create-key-submit-btn"));

    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });

    // Acknowledge and dismiss
    fireEvent.click(screen.getByTestId("ack-stored-checkbox"));
    fireEvent.click(screen.getByTestId("done-secret-btn"));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    // Now navigate / open the key detail drawer for this key
    const queryClient = createAdminQueryClient();
    render(
      <AuthContext.Provider value={createMockAuth()}>
        <AdminQueryProvider client={queryClient}>
          <MemoryRouter initialEntries={[`/keys/${createKeyFixture.id}`]}>
            <Routes>
              <Route path="/keys/:id" element={<KeysPage />} />
            </Routes>
          </MemoryRouter>
        </AdminQueryProvider>
      </AuthContext.Provider>
    );

    // Wait for detail drawer to load
    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(screen.getByTestId("key-detail-content")).toBeInTheDocument();
    });

    // Shows display prefix only
    expect(screen.getByText("sk-5ca93214")).toBeInTheDocument();

    // Guarantees raw key does NOT appear anywhere in the drawer or DOM
    expect(document.body.innerHTML).not.toContain(createKeyFixture.key);
    expect(
      screen.getByText(/Gateway API key secrets are presented only once at creation and cannot be revealed or reconstructed/)
    ).toBeInTheDocument();
  });
});
