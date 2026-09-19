import {
  AdminApiError,
  GenerationMismatchError,
  NetworkError,
  OfflineError,
  OversizedResponseError,
  SafeErrorEnvelope,
  ValidationError,
} from "./errors";
import { ConcurrencyLimiter, defaultConcurrencyLimiter } from "./concurrency";
import { getGeneration } from "./generation";
import { getCsrfToken } from "./csrf";
import { notifyUnauthorized } from "./reauth";

export const DEFAULT_MAX_JSON_BYTES = 2 * 1024 * 1024; // 2 MiB
export const DEFAULT_MAX_BODY_BYTES = 10 * 1024 * 1024; // 10 MiB

export interface AdminFetchOptions<T = unknown> {
  method?: string;
  headers?: Record<string, string>;
  body?: unknown;
  signal?: AbortSignal;
  maxBytes?: number;
  validate?: (raw: unknown) => T;
  responseType?: "json" | "text" | "raw";
  skipRetry?: boolean;
  concurrencyLimiter?: ConcurrencyLimiter;
  allowedStatuses?: number[];
  retryDelayMs?: number;
}

export interface RawResponseResult {
  status: number;
  headers: Headers;
  data: string;
  bytes?: Uint8Array;
}

function isMutationMethod(method: string): boolean {
  const upper = method.toUpperCase();
  return upper === "POST" || upper === "PUT" || upper === "PATCH" || upper === "DELETE";
}

function isTransientStatus(status: number): boolean {
  return status === 502 || status === 503 || status === 504;
}

function sleepWithJitter(
  baseMs = 100,
  jitterMs = 150,
  signal?: AbortSignal
): Promise<void> {
  if (signal?.aborted) {
    return Promise.reject(signal.reason || new Error("Request aborted"));
  }

  const duration = baseMs + (jitterMs > 0 ? Math.floor(Math.random() * jitterMs) : 0);

  return new Promise((resolve, reject) => {
    const onAbort = () => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      reject(signal?.reason || new Error("Request aborted"));
    };

    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, duration);

    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

interface BoundedStreamResult {
  text: string;
  bytes: Uint8Array;
}

async function readBoundedStream(
  response: Response,
  maxBytes: number,
  signal?: AbortSignal
): Promise<BoundedStreamResult> {
  const contentLength = response.headers.get("content-length");
  if (contentLength) {
    const parsedLength = parseInt(contentLength, 10);
    if (!Number.isNaN(parsedLength) && parsedLength > maxBytes) {
      throw new OversizedResponseError(
        `Response size (${parsedLength} bytes) exceeds maximum limit of ${maxBytes} bytes`,
        parsedLength,
        maxBytes
      );
    }
  }

  if (!response.body || typeof response.body.getReader !== "function") {
    let bytes: Uint8Array;
    let text: string;
    if (typeof response.arrayBuffer === "function") {
      const buffer = await response.arrayBuffer();
      if (buffer.byteLength > maxBytes) {
        throw new OversizedResponseError(
          `Response size exceeds maximum limit of ${maxBytes} bytes`,
          buffer.byteLength,
          maxBytes
        );
      }
      bytes = new Uint8Array(buffer);
      text = new TextDecoder("utf-8", { fatal: false }).decode(bytes);
    } else {
      text = await response.text();
      bytes = new TextEncoder().encode(text);
      if (bytes.byteLength > maxBytes) {
        throw new OversizedResponseError(
          `Response size exceeds maximum limit of ${maxBytes} bytes`,
          bytes.byteLength,
          maxBytes
        );
      }
    }
    return { text, bytes };
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let totalBytes = 0;

  try {
    while (true) {
      if (signal?.aborted) {
        await reader.cancel();
        throw signal.reason || new Error("Request aborted");
      }

      const { done, value } = await reader.read();
      if (done) break;

      totalBytes += value.byteLength;
      if (totalBytes > maxBytes) {
        await reader.cancel();
        throw new OversizedResponseError(
          `Response stream exceeded maximum limit of ${maxBytes} bytes`,
          totalBytes,
          maxBytes
        );
      }

      chunks.push(value);
    }

    const combined = new Uint8Array(totalBytes);
    let offset = 0;
    for (const chunk of chunks) {
      combined.set(chunk, offset);
      offset += chunk.byteLength;
    }
    const text = new TextDecoder("utf-8", { fatal: false }).decode(combined);
    return { text, bytes: combined };
  } finally {
    try {
      reader.releaseLock?.();
    } catch {
      // Ignore release lock error if stream is already cancelled/closed
    }
  }
}

export async function adminFetch<T = unknown>(
  url: string,
  options: AdminFetchOptions<T> = {}
): Promise<T> {
  const method = (options.method || "GET").toUpperCase();
  const isMutation = isMutationMethod(method);
  const limiter = options.concurrencyLimiter || defaultConcurrencyLimiter;
  const maxBytes = options.maxBytes ?? (options.responseType === "raw" ? DEFAULT_MAX_BODY_BYTES : DEFAULT_MAX_JSON_BYTES);

  const releaseSlot = await limiter.acquire(isMutation, options.signal);

  try {
    return await executeWithRetry(url, options, method, isMutation, maxBytes);
  } finally {
    releaseSlot();
  }
}

async function executeWithRetry<T>(
  url: string,
  options: AdminFetchOptions<T>,
  method: string,
  isMutation: boolean,
  maxBytes: number
): Promise<T> {
  let attempt = 0;
  const maxAttempts = isMutation || options.skipRetry ? 1 : 2;

  while (attempt < maxAttempts) {
    attempt++;
    const startGeneration = getGeneration();

    if (typeof navigator !== "undefined" && navigator.onLine === false) {
      throw new OfflineError();
    }

    try {
      const response = await doFetch(url, options, method);

      if (getGeneration() !== startGeneration) {
        throw new GenerationMismatchError();
      }

      const isSuccess = response.ok || (options.allowedStatuses?.includes(response.status) ?? false);

      if (!isSuccess) {
        if (response.status === 401) {
          notifyUnauthorized();
        }

        if (!isMutation && attempt < maxAttempts && isTransientStatus(response.status)) {
          try {
            await response.body?.cancel();
          } catch {
            // Ignore cancellation error on retry
          }

          if (typeof navigator !== "undefined" && navigator.onLine === false) {
            throw new OfflineError();
          }
          await sleepWithJitter(
            options.retryDelayMs ?? 100,
            options.retryDelayMs !== undefined ? 0 : 150,
            options.signal
          );
          continue;
        }

        let envelope: SafeErrorEnvelope | null = null;
        try {
          const errResult = await readBoundedStream(response, 64 * 1024, options.signal);
          envelope = JSON.parse(errResult.text) as SafeErrorEnvelope;
        } catch {
          // Keep envelope null if not JSON or read failed
        }

        throw new AdminApiError(response.status, envelope);
      }

      const streamResult = await readBoundedStream(response, maxBytes, options.signal);

      if (getGeneration() !== startGeneration) {
        throw new GenerationMismatchError();
      }

      if (options.responseType === "raw") {
        const rawResult: RawResponseResult = {
          status: response.status,
          headers: response.headers,
          data: streamResult.text,
          bytes: streamResult.bytes,
        };
        return rawResult as unknown as T;
      }

      if (options.responseType === "text") {
        return streamResult.text as unknown as T;
      }

      let parsed: unknown;
      try {
        parsed = JSON.parse(streamResult.text);
      } catch (err) {
        throw new ValidationError("Failed to parse JSON response from admin API", err);
      }

      if (options.validate) {
        return options.validate(parsed);
      }

      return parsed as T;
    } catch (error) {
      if (
        error instanceof AdminApiError ||
        error instanceof GenerationMismatchError ||
        error instanceof ValidationError ||
        error instanceof OversizedResponseError ||
        error instanceof OfflineError
      ) {
        throw error;
      }

      if (options.signal?.aborted || (error instanceof Error && error.name === "AbortError")) {
        throw options.signal?.reason || error;
      }

      if (typeof navigator !== "undefined" && navigator.onLine === false) {
        throw new OfflineError();
      }

      if (!isMutation && attempt < maxAttempts) {
        await sleepWithJitter(
          options.retryDelayMs ?? 100,
          options.retryDelayMs !== undefined ? 0 : 150,
          options.signal
        );
        continue;
      }

      throw new NetworkError(
        error instanceof Error ? error.message : "Network request failed",
        error
      );
    }
  }

  throw new NetworkError("Request failed after maximum retry attempts");
}

async function doFetch<T>(
  url: string,
  options: AdminFetchOptions<T>,
  method: string
): Promise<Response> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...options.headers,
  };

  const isMutation = isMutationMethod(method);
  if (isMutation) {
    const csrf = getCsrfToken();
    if (csrf) {
      headers["X-CSRF-Token"] = csrf;
    }
  }

  let body: BodyInit | null | undefined;
  if (options.body !== undefined && options.body !== null) {
    if (
      typeof options.body === "string" ||
      options.body instanceof Blob ||
      options.body instanceof FormData ||
      options.body instanceof URLSearchParams
    ) {
      body = options.body;
    } else {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(options.body);
    }
  }

  return fetch(url, {
    method,
    headers,
    body,
    credentials: "same-origin",
    signal: options.signal,
  });
}
