import { render, screen, fireEvent, waitFor, within, act } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { onlineManager } from "@tanstack/react-query";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { ToastProvider } from "../../shared/ui";
import { OversizedResponseError } from "../../shared/transport/errors";
import { RequestsPage } from "./RequestsPage";
import { AdminRequestDetail } from "./types";
import { downloadBodyBytes } from "./helpers";

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

function mockOctetStreamResponse(
  bodyBytes: Uint8Array | string,
  originalSize: number,
  truncated = false,
  contentType = "application/octet-stream"
): Response {
  const bytes = typeof bodyBytes === "string" ? new TextEncoder().encode(bodyBytes) : bodyBytes;
  return new Response(bytes as unknown as BodyInit, {
    status: 200,
    headers: {
      "Content-Type": contentType,
      "X-Original-Size": String(originalSize),
      "X-Truncated": String(truncated),
    },
  });
}

const baseRequestDetail: AdminRequestDetail = {
  request_id: "0191eb0b62bc7b7489a2434685ef3b600191eb0b62bc7b7489a2434685ef3b60",
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
  terminal_outcome: "success",
  upstream_started: true,
  error_code: null,
  client_bytes: 1024,
  upstream_bytes: 4096,
  delivered_bytes: 4096,
  input_tokens: 120,
  output_tokens: 450,
  total_tokens: 570,
  cached_input_tokens: 64,
  reasoning_output_tokens: 0,
  cost_micros: 8250,
  started_at: "2026-09-17T14:30:00Z",
  upstream_started_at: "2026-09-17T14:30:00.010Z",
  upstream_headers_at: "2026-09-17T14:30:00.250Z",
  first_byte_at: "2026-09-17T14:30:00.255Z",
  finished_at: "2026-09-17T14:30:01.200Z",
  total_micros: 1200000,
  time_to_upstream_headers_micros: 240000,
  time_to_first_byte_micros: 245000,
  stream_close_delay_micros: 5000,
  has_bodies: ["client_request", "response"],
};

function renderRequestDetail(initialUrl: string) {
  const queryClient = createAdminQueryClient();
  return render(
    <AdminQueryProvider client={queryClient}>
      <ToastProvider>
        <MemoryRouter initialEntries={[initialUrl]}>
          <Routes>
            <Route path="/requests" element={<div data-testid="requests-list-target">Requests List</div>} />
            <Route path="/requests/:id" element={<RequestsPage />} />
            <Route path="/keys/:id" element={<div data-testid="key-detail-target">Key Detail</div>} />
            <Route path="/login" element={<div data-testid="login-target">Login Page</div>} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </AdminQueryProvider>
  );
}

describe("T174 Request Details and Safe Body Viewer", () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    onlineManager.setOnline(true);
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  describe("Metadata Sections & Outcome Variants", () => {
    it("renders successful request detail with full identity, model, accounting, and timeline", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(baseRequestDetail));

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      // Wait for content to arrive
      const reqIdElem = await screen.findByTestId("detail-request-id");
      expect(reqIdElem).toHaveTextContent(baseRequestDetail.request_id);
      expect(screen.getByTestId("detail-api-key")).toHaveTextContent("Production Bot Key");
      expect(screen.getByTestId("detail-route")).toHaveTextContent("POST");
      expect(screen.getByTestId("detail-route")).toHaveTextContent("/v1/chat/completions");
      expect(screen.getByTestId("detail-model")).toHaveTextContent("gpt-4o");

      // Status & Outcome
      expect(screen.getByTestId("detail-downstream-status")).toHaveTextContent("200");
      expect(screen.getByTestId("detail-upstream-status")).toHaveTextContent("200");
      expect(screen.getByTestId("detail-upstream-started")).toHaveTextContent("Started");
      expect(screen.getByTestId("detail-outcome")).toHaveTextContent("200 Success");

      // Accounting
      expect(screen.getByTestId("detail-input-tokens")).toHaveTextContent("120");
      expect(screen.getByTestId("detail-cached-tokens")).toHaveTextContent("64");
      expect(screen.getByTestId("detail-output-tokens")).toHaveTextContent("450");
      expect(screen.getByTestId("detail-reasoning-tokens")).toHaveTextContent("0");
      expect(screen.getByTestId("detail-total-tokens")).toHaveTextContent("570");
      expect(screen.getByTestId("detail-cost")).toHaveTextContent("$0.0083");
      expect(screen.getByTestId("detail-client-bytes")).toHaveTextContent("1 KB");
      expect(screen.getByTestId("detail-upstream-bytes")).toHaveTextContent("4 KB");

      // Timeline
      expect(screen.getByTestId("request-timeline")).toBeInTheDocument();
      expect(screen.getByTestId("timeline-total-duration")).toHaveTextContent("1.2 s");
      expect(screen.getByTestId("timeline-event-started")).toHaveTextContent("Request Received");
      expect(screen.getByTestId("timeline-event-upstream_started")).toHaveTextContent("Upstream Dispatched");
      expect(screen.getByTestId("timeline-event-upstream_headers")).toHaveTextContent("240 ms");
      expect(screen.getByTestId("timeline-event-first_byte")).toHaveTextContent("245 ms");
    });

    it("renders rejected pre-upstream request with skipped upstream steps and error code", async () => {
      const rejectedDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        request_id: "0191eb0b62bc7b7489a2434685ef3b600191eb0b62bc7b7489a2434685ef3b61",
        downstream_status: 403,
        upstream_status: null,
        terminal_outcome: "pre_upstream",
        upstream_started: false,
        error_code: "model_not_allowed",
        upstream_started_at: null,
        upstream_headers_at: null,
        first_byte_at: null,
        time_to_upstream_headers_micros: null,
        time_to_first_byte_micros: null,
        has_bodies: ["client_request"],
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(rejectedDetail));

      renderRequestDetail(`/requests/${rejectedDetail.request_id}`);

      expect(await screen.findByTestId("detail-outcome")).toHaveTextContent("403 Pre-Upstream Rejection");
      expect(screen.getByTestId("detail-upstream-started")).toHaveTextContent("Not Started");
      expect(screen.getByTestId("detail-error-code")).toHaveTextContent("model_not_allowed");
      expect(screen.getByTestId("detail-upstream-status")).toHaveTextContent("—");

      // Upstream timeline events are skipped
      const upstreamHeadersEvent = screen.getByTestId("timeline-event-upstream_headers");
      expect(within(upstreamHeadersEvent).getByText("Skipped")).toBeInTheDocument();
    });

    it("renders cancelled request with honest outcome indicator", async () => {
      const cancelledDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        downstream_status: 499,
        upstream_status: null,
        terminal_outcome: "client_cancelled",
        upstream_started: true,
        error_code: null,
        has_bodies: [],
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(cancelledDetail));

      renderRequestDetail(`/requests/${cancelledDetail.request_id}`);

      expect(await screen.findByTestId("detail-outcome")).toHaveTextContent("499 Client Cancelled");
    });

    it("renders upstream error with danger badge and error code", async () => {
      const upstreamErrorDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        downstream_status: 502,
        upstream_status: 500,
        terminal_outcome: "upstream_error",
        upstream_started: true,
        error_code: "upstream_timeout",
        has_bodies: ["client_request"],
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(upstreamErrorDetail));

      renderRequestDetail(`/requests/${upstreamErrorDetail.request_id}`);

      expect(await screen.findByTestId("detail-outcome")).toHaveTextContent("502 Upstream Error");
      expect(screen.getByTestId("detail-error-code")).toHaveTextContent("upstream_timeout");
    });

    it("renders converted SSE streaming modes (stream -> json)", async () => {
      const convertedDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        requested_mode: "stream",
        upstream_mode: "stream",
        delivered_mode: "json",
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(convertedDetail));

      renderRequestDetail(`/requests/${convertedDetail.request_id}`);

      expect(await screen.findByTestId("detail-modes")).toHaveTextContent("stream → json");
    });

    it("strictly preserves null vs zero for tokens, cost, status, and durations", async () => {
      const nullFieldsDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        downstream_status: null,
        upstream_status: null,
        terminal_outcome: null,
        error_code: null,
        client_bytes: null,
        upstream_bytes: null,
        delivered_bytes: null,
        input_tokens: null,
        output_tokens: null,
        total_tokens: null,
        cached_input_tokens: null,
        reasoning_output_tokens: null,
        cost_micros: null,
        started_at: null,
        finished_at: null,
        total_micros: null,
        time_to_upstream_headers_micros: null,
        time_to_first_byte_micros: null,
        stream_close_delay_micros: null,
        has_bodies: [],
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(nullFieldsDetail));

      renderRequestDetail(`/requests/${nullFieldsDetail.request_id}`);

      expect(await screen.findByTestId("detail-downstream-status")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-upstream-status")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-input-tokens")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-cached-tokens")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-output-tokens")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-reasoning-tokens")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-total-tokens")).toHaveTextContent("—");
      expect(screen.getByTestId("detail-cost")).toHaveTextContent("—");
      expect(screen.getByTestId("timeline-total-duration")).toHaveTextContent("—");
    });

    it("displays contextual notice when no bodies were captured", async () => {
      const noBodiesDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        has_bodies: [],
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(noBodiesDetail));

      renderRequestDetail(`/requests/${noBodiesDetail.request_id}`);

      expect(await screen.findByTestId("no-bodies-notice")).toHaveTextContent(
        "No payload bodies were captured for this request"
      );
      expect(screen.queryByTestId("available-bodies-list")).not.toBeInTheDocument();
    });

    it("displays all available body kinds in list", async () => {
      const allBodiesDetail: AdminRequestDetail = {
        ...baseRequestDetail,
        has_bodies: ["client_request", "upstream_request", "response"],
      };

      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(allBodiesDetail));

      renderRequestDetail(`/requests/${allBodiesDetail.request_id}`);

      expect(await screen.findByTestId("available-body-client_request")).toBeInTheDocument();
      expect(screen.getByTestId("available-body-upstream_request")).toBeInTheDocument();
      expect(screen.getByTestId("available-body-response")).toBeInTheDocument();
    });
  });

  describe("Error States and Navigation", () => {
    it("renders 404 EmptyState when request record is not found or pruned by retention", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { message: "Request not found", code: "not_found" } }), {
          status: 404,
          headers: { "Content-Type": "application/json" },
        })
      );

      renderRequestDetail("/requests/0191eb0b62bc7b7489a2434685ef3b600191eb0b62bc7b7489a2434685ef3b60");

      expect(await screen.findByTestId("detail-404-state")).toBeInTheDocument();
      expect(screen.getByText("Request Not Found")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Return to Requests" })).toBeInTheDocument();
    });

    it("renders 400 alert when request ID is malformed", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { message: "Invalid request id", code: "invalid_request" } }), {
          status: 400,
          headers: { "Content-Type": "application/json" },
        })
      );

      renderRequestDetail("/requests/invalid-short-id");

      expect(await screen.findByTestId("detail-400-alert")).toBeInTheDocument();
      expect(screen.getByText("Invalid Request ID")).toBeInTheDocument();
    });

    it("renders 401 Session Expired alert with Sign In action", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { message: "Unauthorized", code: "unauthorized" } }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        })
      );

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      expect(await screen.findByTestId("detail-401-alert")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Sign In" })).toBeInTheDocument();
    });

    it("renders offline alert when connectivity is lost", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(baseRequestDetail));

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      expect(await screen.findByTestId("request-detail-view")).toBeInTheDocument();

      act(() => {
        onlineManager.setOnline(false);
      });

      expect(await screen.findByTestId("detail-offline-alert")).toBeInTheDocument();
    });

    it("preserves URL history filters in return link", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(baseRequestDetail));

      renderRequestDetail(
        `/requests/${baseRequestDetail.request_id}?key_id=key-prod-01&range=24h&limit=50`
      );

      expect(await screen.findByTestId("detail-back-link")).toBeInTheDocument();
      const backLink = screen.getByTestId("detail-back-link");
      expect(backLink).toHaveAttribute(
        "href",
        "/requests?key_id=key-prod-01&range=24h&limit=50"
      );
    });

    it("copies request ID to clipboard and announces politely to screen readers", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(mockJsonResponse(baseRequestDetail));
      const writeTextMock = vi.fn().mockResolvedValue(undefined);
      Object.assign(navigator, {
        clipboard: { writeText: writeTextMock },
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      const copyBtn = await screen.findByTestId("detail-copy-id-btn");
      fireEvent.click(copyBtn);

      expect(writeTextMock).toHaveBeenCalledWith(baseRequestDetail.request_id);
      expect(screen.getByText("Request ID copied to clipboard")).toBeInTheDocument();
    });
  });

  describe("Safe Body Viewer & Body Kinds", () => {
    it("lazy loads BodyViewer only after operator explicitly clicks 'View Body'", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse('{"model":"gpt-4o","prompt":"hello"}', 34, false, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      expect(await screen.findByTestId("request-detail-view")).toBeInTheDocument();

      // Body viewer is NOT mounted initially!
      expect(screen.queryByTestId("body-viewer")).not.toBeInTheDocument();

      // Operator explicitly clicks "View Body" on client_request
      const viewBtn = await screen.findByTestId("open-body-client_request-btn");
      fireEvent.click(viewBtn);

      // Body viewer is now mounted and fetches body
      expect(await screen.findByTestId("body-viewer")).toBeInTheDocument();
      expect(await screen.findByTestId("body-content-panel")).toBeInTheDocument();
      expect(await screen.findByTestId("body-pre-json")).toHaveTextContent('"model": "gpt-4o"');

      // Operator closes viewer -> viewer is UNMOUNTED!
      const closeBtn = screen.getByTestId("close-body-viewer-btn");
      fireEvent.click(closeBtn);

      expect(screen.queryByTestId("body-viewer")).not.toBeInTheDocument();
    });

    it("fetches each available body kind only on explicit action", async () => {
      const fetchMock = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse('{"prompt":"hello"}', 18, false, "application/json");
        }
        if (urlStr.includes("/bodies/response")) {
          return mockOctetStreamResponse('{"completion":"hi"}', 19, false, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });
      globalThis.fetch = fetchMock;

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      // Open client_request
      const openBtn = await screen.findByTestId("open-body-client_request-btn");
      fireEvent.click(openBtn);
      expect(await screen.findByTestId("body-viewer")).toBeInTheDocument();

      // Verified response body has NOT been fetched yet!
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/bodies/client_request"),
        expect.anything()
      );
      expect(fetchMock).not.toHaveBeenCalledWith(
        expect.stringContaining("/bodies/response"),
        expect.anything()
      );

      // Now switch tab to response
      const responseTab = await screen.findByTestId("body-tab-response");
      fireEvent.click(responseTab);

      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledWith(
          expect.stringContaining("/bodies/response"),
          expect.anything()
        );
      });
    });

    it("renders valid JSON in Pretty JSON, Raw Text, and Hex views", async () => {
      const jsonPayload = '{"model":"gpt-4o","temperature":0.7}';
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(jsonPayload, jsonPayload.length, false, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      expect(await screen.findByTestId("body-pre-json")).toBeInTheDocument();

      // Switch to Raw Text mode
      fireEvent.click(screen.getByTestId("view-mode-text"));
      expect(await screen.findByTestId("body-pre-text")).toHaveTextContent(jsonPayload);

      // Switch to Hex mode
      fireEvent.click(screen.getByTestId("view-mode-hex"));
      const hexPre = await screen.findByTestId("body-pre-hex");
      expect(hexPre).toHaveTextContent("00000000");
      expect(hexPre).toHaveTextContent("7b 22 6d 6f 64 65 6c 22"); // '{"model"' in hex
    });

    it("renders SSE fragments safely", async () => {
      const ssePayload = "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: [DONE]\n\n";
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/response")) {
          return mockOctetStreamResponse(ssePayload, ssePayload.length, false, "text/event-stream");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-response-btn"));

      // Not valid JSON -> defaults to text view
      expect(await screen.findByTestId("body-pre-text")).toHaveTextContent("data: [DONE]");
    });

    it("renders binary and gzip byte sequences safely in Hex view without crashes", async () => {
      // Gzip magic number 0x1f, 0x8b followed by arbitrary binary
      const gzipBytes = new Uint8Array([0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xca, 0x48, 0xcd, 0xc9, 0xc9, 0x07, 0x00]);
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/response")) {
          return mockOctetStreamResponse(gzipBytes, gzipBytes.length, false, "application/gzip");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-response-btn"));

      expect(await screen.findByTestId("body-viewer")).toBeInTheDocument();

      // Switch to Hex view
      fireEvent.click(screen.getByTestId("view-mode-hex"));
      const hexView = await screen.findByTestId("body-pre-hex");
      expect(hexView).toHaveTextContent("1f 8b 08 00");
    });

    it("handles invalid UTF-8 and NUL bytes safely", async () => {
      // Invalid UTF-8 sequence with NUL byte
      const invalidUtf8 = new Uint8Array([0x00, 0xc3, 0x28, 0xff, 0xfe, 0x61, 0x62]);
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(invalidUtf8, invalidUtf8.length, false, "application/octet-stream");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      expect(await screen.findByTestId("body-viewer")).toBeInTheDocument();
      // Hex representation shows accurate offsets and bytes
      fireEvent.click(screen.getByTestId("view-mode-hex"));
      const hexView = await screen.findByTestId("body-pre-hex");
      expect(hexView).toHaveTextContent("00 c3 28 ff fe 61 62");
    });

    it("displays empty notice when body has 0 bytes", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(new Uint8Array(0), 0, false, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      expect(await screen.findByTestId("body-empty-notice")).toHaveTextContent("Captured body is empty (0 bytes)");
    });

    it("displays truncation warning when X-Truncated is true", async () => {
      const captured = '{"truncated":true}';
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(captured, 500000, true, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      expect(await screen.findByTestId("body-truncated-alert")).toHaveTextContent(
        "Payload exceeded the configured body capture limit"
      );
      expect(screen.getByTestId("body-truncated-alert")).toHaveTextContent("500,000 bytes");
    });

    it("bounds preview decoding to 256 KiB and shows warning for oversized bodies", async () => {
      // 300 KiB body (> 256 KiB = 262,144 bytes)
      const oversizedBytes = new Uint8Array(300 * 1024);
      oversizedBytes.fill(65); // 'A'

      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(oversizedBytes, oversizedBytes.length, false, "text/plain");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      expect(await screen.findByTestId("body-preview-bound-alert")).toBeInTheDocument();
      expect(screen.getByTestId("body-preview-bound-alert")).toHaveTextContent("Preview Bounded to 256 KiB");
    });

    it("handles concurrent body retention deletion gracefully with safe alert", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        // Body request returns 404 (retention job pruned the body!)
        if (urlStr.includes("/bodies/")) {
          return new Response(JSON.stringify({ error: { message: "Body pruned", code: "not_found" } }), {
            status: 404,
            headers: { "Content-Type": "application/json" },
          });
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      expect(await screen.findByTestId("body-error-alert")).toBeInTheDocument();
      expect(screen.getByText("Body Not Available")).toBeInTheDocument();
      expect(screen.getByTestId("body-error-alert")).toHaveTextContent(
        "Captured body not found. It may have been removed by history retention"
      );

      // Metadata card is still intact!
      expect(screen.getByTestId("detail-request-id")).toBeInTheDocument();
    });

    it("verifies exact-byte download equality", async () => {
      const originalPayload = '{"exact":"byte_payload_check"}';
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(originalPayload, originalPayload.length, false, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });

      // Spy on URL.createObjectURL, anchor click, and Blob
      let createdBlob: Blob | null = null;
      const originalCreateObjectURL = URL.createObjectURL;
      const originalRevokeObjectURL = URL.revokeObjectURL;

      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

      URL.createObjectURL = vi.fn().mockImplementation((blob: Blob) => {
        createdBlob = blob;
        return "blob:http://localhost/test-uuid";
      });
      URL.revokeObjectURL = vi.fn();

      try {
        renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
        fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

        const downloadBtn = await screen.findByTestId("download-body-btn");
        fireEvent.click(downloadBtn);

        expect(clickSpy).toHaveBeenCalled();
        expect(URL.createObjectURL).toHaveBeenCalled();
        expect(createdBlob).not.toBeNull();
        expect(createdBlob!.size).toBe(originalPayload.length);
        expect(createdBlob!.type).toBe("application/json");

        // Verify direct byte download helper byte-for-byte
        let directBlob: Blob | null = null;
        URL.createObjectURL = vi.fn((b: Blob) => {
          directBlob = b;
          return "blob:mock";
        });
        const exactBytes = new Uint8Array([0x1f, 0x8b, 0x00, 0xff, 0x42]);
        downloadBodyBytes(exactBytes, "download.bin", "application/octet-stream");
        expect(directBlob).not.toBeNull();
        expect(directBlob!.size).toBe(exactBytes.length);
        expect(directBlob!.type).toBe("application/octet-stream");
      } finally {
        URL.createObjectURL = originalCreateObjectURL;
        URL.revokeObjectURL = originalRevokeObjectURL;
        clickSpy.mockRestore();
      }
    });

    it("strictly prevents HTML/script injection from malicious body text", async () => {
      const maliciousPayload = "<script>alert('pwned')</script><img src=x onerror=alert(1)><b>dangerous tag</b>";
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(maliciousPayload, maliciousPayload.length, false, "text/html");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      const pre = await screen.findByTestId("body-pre-text");
      expect(pre).toHaveTextContent("<script>alert('pwned')</script>");

      // Verify no executable <script> or <img> DOM elements were created!
      expect(pre.querySelectorAll("script")).toHaveLength(0);
      expect(pre.querySelectorAll("img")).toHaveLength(0);
      expect(pre.querySelectorAll("b")).toHaveLength(0);
    });

    it("falls back to text view rather than blank when switching from JSON-valid body kind to non-JSON body while viewMode is json", async () => {
      const jsonPayload = '{"prompt":"hello world","model":"gpt-4o"}';
      const ssePayload = "data: {\"chunk\":1}\n\ndata: [DONE]\n\n";

      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return mockOctetStreamResponse(jsonPayload, jsonPayload.length, false, "application/json");
        }
        if (urlStr.includes("/bodies/response")) {
          return mockOctetStreamResponse(ssePayload, ssePayload.length, false, "text/event-stream");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      // Open client_request (valid JSON)
      fireEvent.click(await screen.findByTestId("open-body-client_request-btn"));

      // JSON preview is active initially
      const jsonPre = await screen.findByTestId("body-pre-json");
      expect(jsonPre).toBeInTheDocument();
      expect(jsonPre).toHaveTextContent('"prompt": "hello world"');
      expect(screen.getByTestId("view-mode-json")).toHaveClass("gw-btn--primary");

      // Switch to response (non-JSON SSE stream)
      fireEvent.click(screen.getByTestId("body-tab-response"));

      // Regression check: preview must NOT be blank, must fall back to text view
      const textPre = await screen.findByTestId("body-pre-text");
      expect(textPre).toBeInTheDocument();
      expect(textPre).toHaveTextContent("data: [DONE]");
      expect(screen.queryByTestId("body-pre-json")).not.toBeInTheDocument();
      expect(screen.getByTestId("view-mode-text")).toHaveClass("gw-btn--primary");
      expect(screen.queryByTestId("view-mode-json")).not.toBeInTheDocument();
    });
  });

  describe("Direct Body Download Lifecycle", () => {
    it("aborts and suppresses stale download effects when navigating away", async () => {
      let bodySignal: AbortSignal | undefined;
      let resolveBody: (() => void) | undefined;
      const bodyPending = new Promise<void>((resolve) => {
        resolveBody = resolve;
      });
      globalThis.fetch = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
        const urlStr = String(input);
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          bodySignal = init?.signal ?? undefined;
          await bodyPending;
          return mockOctetStreamResponse("late body", 9, false, "text/plain");
        }
        return new Response("Not found", { status: 404 });
      });
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      const createObjectURLSpy = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:stale");

      try {
        const view = renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);
        fireEvent.click(await screen.findByTestId("download-body-client_request-btn"));
        await waitFor(() => expect(bodySignal).toBeDefined());

        view.unmount();
        expect(bodySignal?.aborted).toBe(true);

        resolveBody?.();
        await act(async () => {
          await Promise.resolve();
        });
        expect(clickSpy).not.toHaveBeenCalled();
        expect(createObjectURLSpy).not.toHaveBeenCalled();
        expect(screen.queryByTestId("toast-item")).not.toBeInTheDocument();
      } finally {
        clickSpy.mockRestore();
        createObjectURLSpy.mockRestore();
      }
    });
  });

  describe("Direct Body Download Error Feedback", () => {
    it("surfaces accessible user feedback when direct body download fails due to retention 404", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          return new Response(
            JSON.stringify({ error: { message: "Body pruned by retention", code: "not_found" } }),
            { status: 404, headers: { "Content-Type": "application/json" } }
          );
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      const downloadBtn = await screen.findByTestId("download-body-client_request-btn");
      fireEvent.click(downloadBtn);

      // Accessible Alert banner is surfaced
      const alert = await screen.findByTestId("body-download-error-alert");
      expect(alert).toBeInTheDocument();
      expect(alert).toHaveAttribute("role", "alert");
      expect(alert).toHaveTextContent("Body Not Found");
      expect(alert).toHaveTextContent("history retention");

      // Shared Toast notification is also surfaced with role="alert"
      const toastItem = await screen.findByTestId("toast-item");
      expect(toastItem).toBeInTheDocument();
      expect(toastItem).toHaveAttribute("role", "alert");
      expect(toastItem).toHaveTextContent("Body Not Found");
    });

    it("surfaces accessible user feedback when direct body download fails due to oversized payload", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          throw new OversizedResponseError("Captured body exceeds 10MB limit", 15000000, 10000000);
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      const downloadBtn = await screen.findByTestId("download-body-client_request-btn");
      fireEvent.click(downloadBtn);

      const alert = await screen.findByTestId("body-download-error-alert");
      expect(alert).toBeInTheDocument();
      expect(alert).toHaveAttribute("role", "alert");
      expect(alert).toHaveTextContent("Body Exceeds Download Limit");
      expect(alert).toHaveTextContent("maximum allowed payload download size");
    });

    it("surfaces accessible user feedback when direct body download fails due to network failure", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          throw new TypeError("Failed to fetch");
        }
        return new Response("Not found", { status: 404 });
      });

      renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

      const downloadBtn = await screen.findByTestId("download-body-client_request-btn");
      fireEvent.click(downloadBtn);

      const alert = await screen.findByTestId("body-download-error-alert");
      expect(alert).toBeInTheDocument();
      expect(alert).toHaveAttribute("role", "alert");
      expect(alert).toHaveTextContent("Network Error");
      expect(alert).toHaveTextContent("network connection error");
    });

    it("allows dismissing the download error alert and clears error on next successful download", async () => {
      let failDownload = true;
      globalThis.fetch = vi.fn().mockImplementation(async (url: string | URL | Request) => {
        const urlStr = typeof url === "string" ? url : url.toString();
        if (urlStr.endsWith(`/requests/${baseRequestDetail.request_id}`)) {
          return mockJsonResponse(baseRequestDetail);
        }
        if (urlStr.includes("/bodies/client_request")) {
          if (failDownload) {
            return new Response(JSON.stringify({ error: { message: "Body pruned", code: "not_found" } }), {
              status: 404,
              headers: { "Content-Type": "application/json" },
            });
          }
          return mockOctetStreamResponse('{"success":true}', 16, false, "application/json");
        }
        return new Response("Not found", { status: 404 });
      });

      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      const originalCreateObjectURL = URL.createObjectURL;
      const originalRevokeObjectURL = URL.revokeObjectURL;
      URL.createObjectURL = vi.fn(() => "blob:mock");
      URL.revokeObjectURL = vi.fn();

      try {
        renderRequestDetail(`/requests/${baseRequestDetail.request_id}`);

        const downloadBtn = await screen.findByTestId("download-body-client_request-btn");
        fireEvent.click(downloadBtn);

        const alert = await screen.findByTestId("body-download-error-alert");
        expect(alert).toBeInTheDocument();

        // Dismiss alert
        const dismissBtn = within(alert).getByRole("button", { name: "Dismiss alert" });
        fireEvent.click(dismissBtn);

        expect(screen.queryByTestId("body-download-error-alert")).not.toBeInTheDocument();

        // Now download succeeds -> error remains cleared
        failDownload = false;
        fireEvent.click(downloadBtn);

        await waitFor(() => {
          expect(clickSpy).toHaveBeenCalled();
        });
        expect(screen.queryByTestId("body-download-error-alert")).not.toBeInTheDocument();
      } finally {
        URL.createObjectURL = originalCreateObjectURL;
        URL.revokeObjectURL = originalRevokeObjectURL;
        clickSpy.mockRestore();
      }
    });
  });
});
