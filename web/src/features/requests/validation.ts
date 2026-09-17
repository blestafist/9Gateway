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

function parseOptionalString(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  return typeof value === "string" ? value : String(value);
}

function parseOptionalNumber(value: unknown): number | null {
  if (value === null || value === undefined) return null;
  const num = Number(value);
  return Number.isNaN(num) ? null : num;
}

export function validateRequestListItem(raw: unknown): AdminRequestListItem {
  if (!isObject(raw)) {
    throw new ValidationError("Request list item must be an object");
  }

  if (typeof raw.request_id !== "string" || !raw.request_id) {
    throw new ValidationError("Request item missing valid request_id");
  }

  return {
    request_id: raw.request_id,
    api_key_id: parseOptionalString(raw.api_key_id),
    api_key_name: parseOptionalString(raw.api_key_name),
    method: parseOptionalString(raw.method),
    path: parseOptionalString(raw.path),
    route: parseOptionalString(raw.route),
    model: parseOptionalString(raw.model),
    requested_mode: parseOptionalString(raw.requested_mode),
    upstream_mode: parseOptionalString(raw.upstream_mode),
    delivered_mode: parseOptionalString(raw.delivered_mode),
    downstream_status: parseOptionalNumber(raw.downstream_status),
    upstream_status: parseOptionalNumber(raw.upstream_status),
    terminal_outcome: parseOptionalString(raw.terminal_outcome),
    upstream_started: Boolean(raw.upstream_started),
    error_code: parseOptionalString(raw.error_code),
    client_bytes: parseOptionalNumber(raw.client_bytes),
    upstream_bytes: parseOptionalNumber(raw.upstream_bytes),
    delivered_bytes: parseOptionalNumber(raw.delivered_bytes),
    input_tokens: parseOptionalNumber(raw.input_tokens),
    output_tokens: parseOptionalNumber(raw.output_tokens),
    total_tokens: parseOptionalNumber(raw.total_tokens),
    cached_input_tokens: parseOptionalNumber(raw.cached_input_tokens),
    reasoning_output_tokens: parseOptionalNumber(raw.reasoning_output_tokens),
    cost_micros: parseOptionalNumber(raw.cost_micros),
    started_at: parseOptionalString(raw.started_at),
    upstream_started_at: parseOptionalString(raw.upstream_started_at),
    upstream_headers_at: parseOptionalString(raw.upstream_headers_at),
    first_byte_at: parseOptionalString(raw.first_byte_at),
    finished_at: parseOptionalString(raw.finished_at),
    total_micros: parseOptionalNumber(raw.total_micros),
    time_to_upstream_headers_micros: parseOptionalNumber(raw.time_to_upstream_headers_micros),
    time_to_first_byte_micros: parseOptionalNumber(raw.time_to_first_byte_micros),
    stream_close_delay_micros: parseOptionalNumber(raw.stream_close_delay_micros),
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

  const has_bodies: RequestBodyKind[] = Array.isArray(rawObj.has_bodies)
    ? rawObj.has_bodies.filter((k): k is RequestBodyKind => typeof k === "string" && VALID_BODY_KINDS.has(k))
    : [];

  return {
    ...base,
    has_bodies,
  };
}

export function validateRequestBodyContent(
  requestId: string,
  kind: RequestBodyKind,
  raw: { status: number; headers: Headers; data: string }
): RequestBodyContent {
  const originalSizeHeader = raw.headers.get("X-Original-Size");
  const truncatedHeader = raw.headers.get("X-Truncated");
  const contentType = raw.headers.get("Content-Type") || "application/octet-stream";

  const originalSize = originalSizeHeader ? parseInt(originalSizeHeader, 10) : raw.data.length;
  const truncated = truncatedHeader === "true";

  return {
    request_id: requestId,
    kind,
    original_size: Number.isNaN(originalSize) ? raw.data.length : originalSize,
    truncated,
    content_type: contentType,
    data: raw.data,
  };
}
