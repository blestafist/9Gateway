import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  adminFetch,
  AdminApiError,
  OversizedResponseError,
  ValidationError,
  OfflineError,
  GenerationMismatchError,
  setCsrfTokenProvider,
  setUnauthorizedListener,
  bumpGeneration,
  resetGeneration,
  ConcurrencyLimiter,
} from "./index";

describe("Admin Fetch Transport", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    resetGeneration();
    setCsrfTokenProvider(null);
    setUnauthorizedListener(null);
    vi.restoreAllMocks();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  describe("Credentials and Ephemeral CSRF", () => {
    it("sends credentials: 'same-origin' on every request", async () => {
      const mockFetch = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      );
      globalThis.fetch = mockFetch;

      await adminFetch("/admin/v1/test");

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const callArgs = mockFetch.mock.calls[0];
      expect(callArgs?.[1]?.credentials).toBe("same-origin");
    });

    it("attaches X-CSRF-Token for mutations when token is available", async () => {
      setCsrfTokenProvider(() => "test-csrf-token-123");

      const mockFetch = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ created: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      );
      globalThis.fetch = mockFetch;

      await adminFetch("/admin/v1/keys", {
        method: "POST",
        body: { name: "New Key" },
      });

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const headers = (mockFetch.mock.calls[0]?.[1]?.headers as Record<string, string>) ?? {};
      expect(headers["X-CSRF-Token"]).toBe("test-csrf-token-123");
    });

    it("does not attach X-CSRF-Token on GET requests", async () => {
      setCsrfTokenProvider(() => "test-csrf-token-123");

      const mockFetch = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ keys: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      );
      globalThis.fetch = mockFetch;

      await adminFetch("/admin/v1/keys", { method: "GET" });

      const headers = (mockFetch.mock.calls[0]?.[1]?.headers as Record<string, string>) ?? {};
      expect(headers["X-CSRF-Token"]).toBeUndefined();
    });
  });

  describe("Typed Status Errors and Envelope Decoding", () => {
    it("maps 401 to AdminApiError and marks isAuthError", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              message: "Invalid admin credential",
              type: "authentication_error",
              code: "unauthorized",
            },
          }),
          { status: 401, headers: { "Content-Type": "application/json" } }
        )
      );

      await expect(adminFetch("/admin/v1/keys")).rejects.toSatisfy((err: unknown) => {
        return (
          err instanceof AdminApiError &&
          err.status === 401 &&
          err.isAuthError === true &&
          err.code === "unauthorized" &&
          err.message === "Invalid admin credential"
        );
      });
    });

    it("maps 404 to AdminApiError and marks isNotFoundError", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              message: "Key not found",
              type: "invalid_request_error",
              code: "resource_not_found",
            },
          }),
          { status: 404, headers: { "Content-Type": "application/json" } }
        )
      );

      await expect(adminFetch("/admin/v1/keys/nonexistent")).rejects.toSatisfy((err: unknown) => {
        return (
          err instanceof AdminApiError &&
          err.status === 404 &&
          err.isNotFoundError === true &&
          err.code === "resource_not_found"
        );
      });
    });

    it("maps 409 to AdminApiError and marks isConflictError", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              message: "Key name conflict",
              type: "invalid_request_error",
              code: "key_name_conflict",
            },
          }),
          { status: 409, headers: { "Content-Type": "application/json" } }
        )
      );

      await expect(
        adminFetch("/admin/v1/keys", { method: "POST", body: { name: "Duplicate" } })
      ).rejects.toSatisfy((err: unknown) => {
        return (
          err instanceof AdminApiError &&
          err.status === 409 &&
          err.isConflictError === true
        );
      });
    });

    it("maps 429 to AdminApiError and marks isRateLimitError", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              message: "Rate limit exceeded",
              type: "rate_limit_error",
              code: "rate_limited",
            },
          }),
          { status: 429, headers: { "Content-Type": "application/json" } }
        )
      );

      await expect(adminFetch("/admin/v1/keys")).rejects.toSatisfy((err: unknown) => {
        return (
          err instanceof AdminApiError &&
          err.status === 429 &&
          err.isRateLimitError === true
        );
      });
    });

    it("detects cursor_expired error code", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              message: "Cursor has expired",
              type: "invalid_request_error",
              code: "cursor_expired",
            },
          }),
          { status: 400, headers: { "Content-Type": "application/json" } }
        )
      );

      await expect(adminFetch("/admin/v1/requests?cursor=old")).rejects.toSatisfy(
        (err: unknown) => {
          return err instanceof AdminApiError && err.isCursorExpired === true;
        }
      );
    });

    it("maps 500 to AdminApiError and marks isServerError", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              message: "Internal gateway database error",
              type: "api_error",
              code: "internal_error",
            },
          }),
          { status: 500, headers: { "Content-Type": "application/json" } }
        )
      );

      await expect(adminFetch("/admin/v1/keys")).rejects.toSatisfy((err: unknown) => {
        return (
          err instanceof AdminApiError &&
          err.status === 500 &&
          err.isServerError === true
        );
      });
    });

    it("throws ValidationError when 200 OK response contains invalid JSON", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response("not-valid-json-{{{", {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      );

      await expect(adminFetch("/admin/v1/keys")).rejects.toBeInstanceOf(ValidationError);
    });
  });

  describe("401 Re-Auth Transition", () => {
    it("notifies unauthorized listener exactly once during concurrent 401s", async () => {
      const listener = vi.fn();
      setUnauthorizedListener(listener);

      globalThis.fetch = vi.fn().mockImplementation(async () => {
        return new Response(
          JSON.stringify({ error: { message: "Session expired", code: "unauthorized" } }),
          { status: 401, headers: { "Content-Type": "application/json" } }
        );
      });

      // Fire 3 requests in parallel
      const results = await Promise.allSettled([
        adminFetch("/admin/v1/keys"),
        adminFetch("/admin/v1/requests"),
        adminFetch("/admin/v1/keys/k1"),
      ]);

      expect(results.every((r) => r.status === "rejected")).toBe(true);
      expect(listener).toHaveBeenCalledTimes(1);
    });
  });

  describe("Bounded Response Reads and Oversized Payloads", () => {
    it("rejects immediately when Content-Length exceeds maxBytes", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response("x".repeat(100), {
          status: 200,
          headers: {
            "Content-Length": "1000000",
            "Content-Type": "application/json",
          },
        })
      );

      await expect(
        adminFetch("/admin/v1/keys", { maxBytes: 500 })
      ).rejects.toBeInstanceOf(OversizedResponseError);
    });

    it("rejects streaming response when chunk accumulation exceeds maxBytes", async () => {
      const chunk = new TextEncoder().encode("1234567890");
      let sentChunks = 0;
      const stream = new ReadableStream({
        pull(controller) {
          if (sentChunks < 5) {
            controller.enqueue(chunk);
            sentChunks++;
          } else {
            controller.close();
          }
        },
      });

      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(stream, {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      );

      await expect(
        adminFetch("/admin/v1/keys", { maxBytes: 25 })
      ).rejects.toBeInstanceOf(OversizedResponseError);
    });
  });

  describe("Retry Policy", () => {
    it("retries transient 503 GET request at most once with bounded delay", async () => {
      let callCount = 0;
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        callCount++;
        if (callCount === 1) {
          return new Response("Service temporarily overloaded", { status: 503 });
        }
        return new Response(JSON.stringify({ success: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      });

      const result = await adminFetch<{ success: boolean }>("/admin/v1/keys", {
        method: "GET",
        retryDelayMs: 1,
      });

      expect(callCount).toBe(2);
      expect(result.success).toBe(true);
    });

    it("retries GET on network error at most once", async () => {
      let callCount = 0;
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        callCount++;
        if (callCount === 1) {
          throw new TypeError("Failed to fetch");
        }
        return new Response(JSON.stringify({ recovered: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      });

      const result = await adminFetch<{ recovered: boolean }>("/admin/v1/keys", {
        method: "GET",
        retryDelayMs: 1,
      });

      expect(callCount).toBe(2);
      expect(result.recovered).toBe(true);
    });

    it("never retries mutations (POST/PUT/PATCH/DELETE)", async () => {
      let callCount = 0;
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        callCount++;
        return new Response("Bad Gateway", { status: 502 });
      });

      await expect(
        adminFetch("/admin/v1/keys", {
          method: "POST",
          body: { name: "New Key" },
          retryDelayMs: 1,
        })
      ).rejects.toBeInstanceOf(AdminApiError);

      expect(callCount).toBe(1);
    });

    it("does not retry when navigator is offline", async () => {
      const originalNavigator = globalThis.navigator;
      Object.defineProperty(globalThis, "navigator", {
        value: { onLine: false },
        configurable: true,
      });

      let callCount = 0;
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        callCount++;
        throw new TypeError("Network unavailable");
      });

      try {
        await expect(
          adminFetch("/admin/v1/keys", { method: "GET", retryDelayMs: 1 })
        ).rejects.toBeInstanceOf(OfflineError);

        expect(callCount).toBe(0);
      } finally {
        Object.defineProperty(globalThis, "navigator", {
          value: originalNavigator,
          configurable: true,
        });
      }
    });
  });

  describe("AbortSignal and Cancellation", () => {
    it("rejects immediately if signal is already aborted", async () => {
      const controller = new AbortController();
      controller.abort();

      const mockFetch = vi.fn();
      globalThis.fetch = mockFetch;

      await expect(
        adminFetch("/admin/v1/keys", { signal: controller.signal })
      ).rejects.toThrow();

      expect(mockFetch).not.toHaveBeenCalled();
    });

    it("cancels reader when signal aborts during stream reading", async () => {
      const controller = new AbortController();
      const cancelSpy = vi.fn();
      const stream = new ReadableStream({
        pull(streamController) {
          streamController.enqueue(new TextEncoder().encode('{"k":'));
          controller.abort();
        },
        cancel: cancelSpy,
      });

      globalThis.fetch = vi.fn().mockResolvedValue(
        new Response(stream, {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      );

      await expect(
        adminFetch("/admin/v1/keys", { signal: controller.signal })
      ).rejects.toThrow();

      expect(cancelSpy).toHaveBeenCalled();
    });
  });

  describe("Concurrency Limiter", () => {
    it("enforces max 4 concurrent reads and queues excess", async () => {
      const limiter = new ConcurrencyLimiter(4, 1);
      const releaseFns: Array<() => void> = [];

      // Acquire 4 read slots
      for (let i = 0; i < 4; i++) {
        const release = await limiter.acquire(false);
        releaseFns.push(release);
      }

      expect(limiter.activeReadCount).toBe(4);
      expect(limiter.queuedReadCount).toBe(0);

      // 5th read must queue
      let fifthResolved = false;
      const fifthPromise = limiter.acquire(false).then((rel) => {
        fifthResolved = true;
        releaseFns.push(rel);
      });

      expect(fifthResolved).toBe(false);
      expect(limiter.queuedReadCount).toBe(1);

      // Release one slot
      releaseFns[0]?.();
      await fifthPromise;

      expect(fifthResolved).toBe(true);
      expect(limiter.activeReadCount).toBe(4);
      expect(limiter.queuedReadCount).toBe(0);

      // Cleanup
      limiter.reset();
    });

    it("enforces max 1 concurrent mutation", async () => {
      const limiter = new ConcurrencyLimiter(4, 1);

      const release1 = await limiter.acquire(true);
      expect(limiter.activeMutationCount).toBe(1);

      let secondResolved = false;
      const secondPromise = limiter.acquire(true).then((rel) => {
        secondResolved = true;
        rel();
      });

      expect(secondResolved).toBe(false);
      expect(limiter.queuedMutationCount).toBe(1);

      release1();
      await secondPromise;
      expect(secondResolved).toBe(true);

      limiter.reset();
    });

    it("removes queued task when its AbortSignal fires while waiting", async () => {
      const limiter = new ConcurrencyLimiter(1, 1);
      const release1 = await limiter.acquire(false);

      const controller = new AbortController();
      const queuedPromise = limiter.acquire(false, controller.signal);

      expect(limiter.queuedReadCount).toBe(1);
      controller.abort();

      await expect(queuedPromise).rejects.toThrow();
      expect(limiter.queuedReadCount).toBe(0);

      release1();
    });
  });

  describe("Generation Barrier and Supersession", () => {
    it("discards late response when generation changes during in-flight fetch", async () => {
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        // Simulate logout / generation bump while request is in flight
        bumpGeneration();
        return new Response(JSON.stringify({ secret: "data" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      });

      await expect(adminFetch("/admin/v1/keys")).rejects.toBeInstanceOf(
        GenerationMismatchError
      );
    });
  });
});
