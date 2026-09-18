import { ValidationError } from "../../shared/transport";
import {
  AdminOverviewResponse,
  OverviewAggregate,
  OverviewKeyCounts,
  OverviewRecentRequest,
} from "./types";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseRequiredNumber(value: unknown, fieldName: string): number {
  if (typeof value !== "number" || Number.isNaN(value)) {
    throw new ValidationError(`Expected ${fieldName} to be a number, got ${typeof value}`);
  }
  return value;
}

function parseOptionalNumber(value: unknown, fieldName: string): number | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "number" || Number.isNaN(value)) {
    throw new ValidationError(`Expected ${fieldName} to be a number or null, got ${typeof value}`);
  }
  return value;
}

function parseRequiredString(value: unknown, fieldName: string): string {
  if (typeof value !== "string" || !value) {
    throw new ValidationError(`Expected ${fieldName} to be a non-empty string, got ${typeof value}`);
  }
  return value;
}

function parseOptionalString(value: unknown, fieldName: string): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") {
    throw new ValidationError(`Expected ${fieldName} to be a string or null, got ${typeof value}`);
  }
  return value;
}

export function validateOverviewAggregate(raw: unknown, fieldName = "aggregate"): OverviewAggregate {
  if (!isObject(raw)) {
    throw new ValidationError(`${fieldName} must be an object`);
  }

  const total_requests = parseRequiredNumber(
    raw.total_requests !== undefined ? raw.total_requests : raw.requests,
    `${fieldName}.total_requests`
  );
  const requests = parseRequiredNumber(
    raw.requests !== undefined ? raw.requests : total_requests,
    `${fieldName}.requests`
  );
  const successful_requests = parseRequiredNumber(raw.successful_requests, `${fieldName}.successful_requests`);
  const error_requests = parseRequiredNumber(raw.error_requests, `${fieldName}.error_requests`);
  const rejected_requests = parseRequiredNumber(raw.rejected_requests, `${fieldName}.rejected_requests`);

  return {
    requests,
    total_requests,
    successful_requests,
    error_requests,
    rejected_requests,
    input_tokens: parseOptionalNumber(raw.input_tokens, `${fieldName}.input_tokens`),
    cached_input_tokens: parseOptionalNumber(raw.cached_input_tokens, `${fieldName}.cached_input_tokens`),
    output_tokens: parseOptionalNumber(raw.output_tokens, `${fieldName}.output_tokens`),
    cost_micros: parseOptionalNumber(raw.cost_micros, `${fieldName}.cost_micros`),
  };
}

export function validateOverviewKeyCounts(raw: unknown): OverviewKeyCounts {
  if (!isObject(raw)) {
    throw new ValidationError("key_counts must be an object");
  }

  const total = parseRequiredNumber(
    raw.total !== undefined ? raw.total : raw.total_keys,
    "key_counts.total"
  );
  const enabled = parseRequiredNumber(
    raw.enabled !== undefined ? raw.enabled : raw.enabled_keys,
    "key_counts.enabled"
  );

  return {
    total,
    enabled,
    total_keys: total,
    enabled_keys: enabled,
  };
}

export function validateOverviewRecentRequest(raw: unknown): OverviewRecentRequest {
  if (!isObject(raw)) {
    throw new ValidationError("recent_request item must be an object");
  }

  if (typeof raw.request_id !== "string" || !raw.request_id) {
    throw new ValidationError("recent_request missing valid request_id");
  }

  if (typeof raw.upstream_started !== "boolean") {
    throw new ValidationError("recent_request upstream_started must be a boolean");
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

export function validateOverviewResponse(raw: unknown): AdminOverviewResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Overview response must be an object");
  }

  const current_range_start = parseRequiredString(raw.current_range_start, "current_range_start");
  const current_range_end = parseRequiredString(raw.current_range_end, "current_range_end");
  const previous_range_start = parseRequiredString(raw.previous_range_start, "previous_range_start");
  const previous_range_end = parseRequiredString(raw.previous_range_end, "previous_range_end");
  const data_timestamp = parseRequiredString(raw.data_timestamp, "data_timestamp");

  const current = validateOverviewAggregate(raw.current, "current");
  const previous = validateOverviewAggregate(raw.previous, "previous");

  const active_requests = parseRequiredNumber(raw.active_requests, "active_requests");
  const key_counts = validateOverviewKeyCounts(raw.key_counts);

  if (!Array.isArray(raw.recent_requests)) {
    throw new ValidationError("Overview recent_requests must be an array");
  }
  const recent_requests = raw.recent_requests.map((r) => validateOverviewRecentRequest(r));

  const total_requests = parseRequiredNumber(
    raw.total_requests !== undefined ? raw.total_requests : raw.requests !== undefined ? raw.requests : current.total_requests,
    "total_requests"
  );
  const requests = parseRequiredNumber(
    raw.requests !== undefined ? raw.requests : total_requests,
    "requests"
  );
  const successful_requests = parseRequiredNumber(
    raw.successful_requests !== undefined ? raw.successful_requests : current.successful_requests,
    "successful_requests"
  );
  const error_requests = parseRequiredNumber(
    raw.error_requests !== undefined ? raw.error_requests : current.error_requests,
    "error_requests"
  );
  const rejected_requests = parseRequiredNumber(
    raw.rejected_requests !== undefined ? raw.rejected_requests : current.rejected_requests,
    "rejected_requests"
  );
  const input_tokens = parseOptionalNumber(
    raw.input_tokens !== undefined ? raw.input_tokens : current.input_tokens,
    "input_tokens"
  );
  const cached_input_tokens = parseOptionalNumber(
    raw.cached_input_tokens !== undefined ? raw.cached_input_tokens : current.cached_input_tokens,
    "cached_input_tokens"
  );
  const output_tokens = parseOptionalNumber(
    raw.output_tokens !== undefined ? raw.output_tokens : current.output_tokens,
    "output_tokens"
  );
  const cost_micros = parseOptionalNumber(
    raw.cost_micros !== undefined ? raw.cost_micros : current.cost_micros,
    "cost_micros"
  );

  return {
    current_range_start,
    current_range_end,
    previous_range_start,
    previous_range_end,
    data_timestamp,
    current,
    previous,
    requests,
    total_requests,
    successful_requests,
    error_requests,
    rejected_requests,
    input_tokens,
    cached_input_tokens,
    output_tokens,
    cost_micros,
    active_requests,
    key_counts,
    recent_requests,
  };
}
