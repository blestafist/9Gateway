import { ValidationError } from "../../shared/transport";
import {
  AdminRequestDetail,
  AdminRequestListItem,
  AdminRequestListResponse,
  RequestBodyContent,
  RequestBodyKind,
} from "./types";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseOptionalString(value: unknown, fieldName = "field"): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") {
    throw new ValidationError(`Expected ${fieldName} to be a string or null, got ${typeof value}`);
  }
  return value;
}

function parseOptionalNumber(value: unknown, fieldName = "field"): number | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "number" || Number.isNaN(value)) {
    throw new ValidationError(`Expected ${fieldName} to be a number or null, got ${typeof value}`);
  }
  return value;
}

export function validateRequestListItem(raw: unknown): AdminRequestListItem {
  if (!isObject(raw)) {
    throw new ValidationError("Request list item must be an object");
  }

  if (typeof raw.request_id !== "string" || !raw.request_id) {
    throw new ValidationError("Request item missing valid request_id");
  }

  if (typeof raw.upstream_started !== "boolean") {
    throw new ValidationError("Request item upstream_started must be a boolean");
  }

  return {
    request_id: raw.request_id,
    api_key_id: parseOptionalString(raw.api_key_id, "api_key_id"),
    api_key_name: parseOptionalString(raw.api_key_name, "api_key_name"),
    method: parseOptionalString(raw.method, "method"),
    path: parseOptionalString(raw.path, "path"),
    route: parseOptionalString(raw.route, "route"),
    model: parseOptionalString(raw.model, "model"),
    requested_mode: parseOptionalString(raw.requested_mode, "requested_mode"),
    upstream_mode: parseOptionalString(raw.upstream_mode, "upstream_mode"),
    delivered_mode: parseOptionalString(raw.delivered_mode, "delivered_mode"),
    downstream_status: parseOptionalNumber(raw.downstream_status, "downstream_status"),
    upstream_status: parseOptionalNumber(raw.upstream_status, "upstream_status"),
    terminal_outcome: parseOptionalString(raw.terminal_outcome, "terminal_outcome"),
    upstream_started: raw.upstream_started,
    error_code: parseOptionalString(raw.error_code, "error_code"),
    client_bytes: parseOptionalNumber(raw.client_bytes, "client_bytes"),
    upstream_bytes: parseOptionalNumber(raw.upstream_bytes, "upstream_bytes"),
    delivered_bytes: parseOptionalNumber(raw.delivered_bytes, "delivered_bytes"),
    input_tokens: parseOptionalNumber(raw.input_tokens, "input_tokens"),
    output_tokens: parseOptionalNumber(raw.output_tokens, "output_tokens"),
    total_tokens: parseOptionalNumber(raw.total_tokens, "total_tokens"),
    cached_input_tokens: parseOptionalNumber(raw.cached_input_tokens, "cached_input_tokens"),
    reasoning_output_tokens: parseOptionalNumber(raw.reasoning_output_tokens, "reasoning_output_tokens"),
    cost_micros: parseOptionalNumber(raw.cost_micros, "cost_micros"),
    started_at: parseOptionalString(raw.started_at, "started_at"),
    upstream_started_at: parseOptionalString(raw.upstream_started_at, "upstream_started_at"),
    upstream_headers_at: parseOptionalString(raw.upstream_headers_at, "upstream_headers_at"),
    first_byte_at: parseOptionalString(raw.first_byte_at, "first_byte_at"),
    finished_at: parseOptionalString(raw.finished_at, "finished_at"),
    total_micros: parseOptionalNumber(raw.total_micros, "total_micros"),
    time_to_upstream_headers_micros: parseOptionalNumber(raw.time_to_upstream_headers_micros, "time_to_upstream_headers_micros"),
    time_to_first_byte_micros: parseOptionalNumber(raw.time_to_first_byte_micros, "time_to_first_byte_micros"),
    stream_close_delay_micros: parseOptionalNumber(raw.stream_close_delay_micros, "stream_close_delay_micros"),
  };
}

export function validateRequestListResponse(raw: unknown): AdminRequestListResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Request list response must be an object");
  }

  if (!Array.isArray(raw.requests)) {
    throw new ValidationError("Request list response requests must be an array");
  }

  const requests = raw.requests.map((r) => validateRequestListItem(r));

  if (raw.next_cursor !== undefined && raw.next_cursor !== null && typeof raw.next_cursor !== "string") {
    throw new ValidationError("Request list response next_cursor must be a string or undefined");
  }
  const next_cursor =
    typeof raw.next_cursor === "string" ? raw.next_cursor : undefined;

  return {
    requests,
    next_cursor,
  };
}

const VALID_BODY_KINDS = new Set<string>([
  "client_request",
  "upstream_request",
  "response",
]);

export function validateRequestDetail(raw: unknown): AdminRequestDetail {
  const base = validateRequestListItem(raw);
  const rawObj = raw as Record<string, unknown>;

  if (rawObj.has_bodies !== undefined) {
    if (!Array.isArray(rawObj.has_bodies)) {
      throw new ValidationError("Request detail has_bodies must be an array");
    }
    for (const k of rawObj.has_bodies) {
      if (typeof k !== "string" || !VALID_BODY_KINDS.has(k)) {
        throw new ValidationError(`Invalid body kind in has_bodies: ${String(k)}`);
      }
    }
  }

  const has_bodies: RequestBodyKind[] = Array.isArray(rawObj.has_bodies)
    ? (rawObj.has_bodies as RequestBodyKind[])
    : [];

  return {
    ...base,
    has_bodies,
  };
}

export function validateRequestBodyContent(
  requestId: string,
  kind: RequestBodyKind,
  raw: { status: number; headers: Headers; data: string; bytes?: Uint8Array }
): RequestBodyContent {
  const originalSizeHeader = raw.headers.get("X-Original-Size");
  const truncatedHeader = raw.headers.get("X-Truncated");
  const contentType = raw.headers.get("Content-Type") || "application/octet-stream";

  const bytes = raw.bytes ?? new TextEncoder().encode(raw.data);
  const originalSize = originalSizeHeader ? parseInt(originalSizeHeader, 10) : bytes.length;
  const truncated = truncatedHeader === "true";

  return {
    request_id: requestId,
    kind,
    original_size: Number.isNaN(originalSize) ? bytes.length : originalSize,
    truncated,
    content_type: contentType,
    data: raw.data,
    bytes,
  };
}
