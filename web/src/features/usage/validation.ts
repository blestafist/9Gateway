import { ValidationError } from "../../shared/transport";
import {
  UsageTimeseriesResponse,
  UsageTimeseriesBucket,
  UsageBreakdownResponse,
  UsageBreakdownRow,
  UsageBreakdownTotal,
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

function parseRequiredBoolean(value: unknown, fieldName: string): boolean {
  if (typeof value !== "boolean") {
    throw new ValidationError(`Expected ${fieldName} to be a boolean, got ${typeof value}`);
  }
  return value;
}

export function validateUsageBucket(raw: unknown, index: number): UsageTimeseriesBucket {
  if (!isObject(raw)) {
    throw new ValidationError(`Bucket at index ${index} must be an object`);
  }

  return {
    bucket_start: parseRequiredString(raw.bucket_start, `buckets[${index}].bucket_start`),
    bucket_end: parseRequiredString(raw.bucket_end, `buckets[${index}].bucket_end`),
    total_requests: parseRequiredNumber(raw.total_requests, `buckets[${index}].total_requests`),
    successful_requests: parseRequiredNumber(
      raw.successful_requests,
      `buckets[${index}].successful_requests`
    ),
    error_requests: parseRequiredNumber(raw.error_requests, `buckets[${index}].error_requests`),
    rejected_requests: parseRequiredNumber(
      raw.rejected_requests,
      `buckets[${index}].rejected_requests`
    ),
    input_tokens: parseRequiredNumber(raw.input_tokens, `buckets[${index}].input_tokens`),
    cached_input_tokens: parseRequiredNumber(
      raw.cached_input_tokens,
      `buckets[${index}].cached_input_tokens`
    ),
    output_tokens: parseRequiredNumber(raw.output_tokens, `buckets[${index}].output_tokens`),
    cost_micros: parseOptionalNumber(raw.cost_micros, `buckets[${index}].cost_micros`),
    avg_total_latency_micros: parseOptionalNumber(
      raw.avg_total_latency_micros,
      `buckets[${index}].avg_total_latency_micros`
    ),
    total_latency_samples: parseRequiredNumber(
      raw.total_latency_samples,
      `buckets[${index}].total_latency_samples`
    ),
    avg_ttfb_latency_micros: parseOptionalNumber(
      raw.avg_ttfb_latency_micros,
      `buckets[${index}].avg_ttfb_latency_micros`
    ),
    ttfb_latency_samples: parseRequiredNumber(
      raw.ttfb_latency_samples,
      `buckets[${index}].ttfb_latency_samples`
    ),
    avg_upstream_latency_micros: parseOptionalNumber(
      raw.avg_upstream_latency_micros,
      `buckets[${index}].avg_upstream_latency_micros`
    ),
    upstream_latency_samples: parseRequiredNumber(
      raw.upstream_latency_samples,
      `buckets[${index}].upstream_latency_samples`
    ),
  };
}

export function validateUsageTimeseriesResponse(raw: unknown): UsageTimeseriesResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Usage timeseries response must be an object");
  }

  if (!Array.isArray(raw.buckets)) {
    throw new ValidationError("Usage timeseries response must have a buckets array");
  }

  return {
    requested_after: parseOptionalString(raw.requested_after, "requested_after"),
    effective_after: parseRequiredString(raw.effective_after, "effective_after"),
    before: parseRequiredString(raw.before, "before"),
    bucket: parseRequiredString(raw.bucket, "bucket"),
    retention_limited: parseRequiredBoolean(raw.retention_limited, "retention_limited"),
    earliest_retained_at: parseOptionalString(raw.earliest_retained_at, "earliest_retained_at"),
    latest_retained_at: parseOptionalString(raw.latest_retained_at, "latest_retained_at"),
    buckets: raw.buckets.map((b, idx) => validateUsageBucket(b, idx)),
  };
}

export function validateUsageBreakdownRow(raw: unknown, fieldName: string): UsageBreakdownRow {
  if (!isObject(raw)) {
    throw new ValidationError(`${fieldName} must be an object`);
  }

  return {
    id: typeof raw.id === "string" ? raw.id : "",
    name: typeof raw.name === "string" ? raw.name : "",
    key_id: typeof raw.key_id === "string" ? raw.key_id : undefined,
    is_unknown: Boolean(raw.is_unknown),
    is_deleted: Boolean(raw.is_deleted),
    total_requests: parseRequiredNumber(raw.total_requests, `${fieldName}.total_requests`),
    successful_requests: parseRequiredNumber(
      raw.successful_requests,
      `${fieldName}.successful_requests`
    ),
    error_requests: parseRequiredNumber(raw.error_requests, `${fieldName}.error_requests`),
    rejected_requests: parseRequiredNumber(
      raw.rejected_requests,
      `${fieldName}.rejected_requests`
    ),
    input_tokens: parseRequiredNumber(raw.input_tokens, `${fieldName}.input_tokens`),
    cached_input_tokens: parseRequiredNumber(
      raw.cached_input_tokens,
      `${fieldName}.cached_input_tokens`
    ),
    output_tokens: parseRequiredNumber(raw.output_tokens, `${fieldName}.output_tokens`),
    cost_micros: parseOptionalNumber(raw.cost_micros, `${fieldName}.cost_micros`),
  };
}

export function validateUsageBreakdownTotal(raw: unknown, fieldName: string): UsageBreakdownTotal {
  if (!isObject(raw)) {
    throw new ValidationError(`${fieldName} must be an object`);
  }

  return {
    total_requests: parseRequiredNumber(raw.total_requests, `${fieldName}.total_requests`),
    successful_requests: parseRequiredNumber(
      raw.successful_requests,
      `${fieldName}.successful_requests`
    ),
    error_requests: parseRequiredNumber(raw.error_requests, `${fieldName}.error_requests`),
    rejected_requests: parseRequiredNumber(
      raw.rejected_requests,
      `${fieldName}.rejected_requests`
    ),
    input_tokens: parseRequiredNumber(raw.input_tokens, `${fieldName}.input_tokens`),
    cached_input_tokens: parseRequiredNumber(
      raw.cached_input_tokens,
      `${fieldName}.cached_input_tokens`
    ),
    output_tokens: parseRequiredNumber(raw.output_tokens, `${fieldName}.output_tokens`),
    cost_micros: parseOptionalNumber(raw.cost_micros, `${fieldName}.cost_micros`),
  };
}

export function validateUsageBreakdownResponse(raw: unknown): UsageBreakdownResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Usage breakdown response must be an object");
  }

  if (!Array.isArray(raw.rows)) {
    throw new ValidationError("Usage breakdown response must have a rows array");
  }

  return {
    requested_after: parseOptionalString(raw.requested_after, "requested_after"),
    effective_after: parseRequiredString(raw.effective_after, "effective_after"),
    before: parseRequiredString(raw.before, "before"),
    group_by: parseRequiredString(raw.group_by, "group_by"),
    retention_limited: parseRequiredBoolean(raw.retention_limited, "retention_limited"),
    earliest_retained_at: parseOptionalString(raw.earliest_retained_at, "earliest_retained_at"),
    latest_retained_at: parseOptionalString(raw.latest_retained_at, "latest_retained_at"),
    rows: raw.rows.map((r, idx) => validateUsageBreakdownRow(r, `rows[${idx}]`)),
    other: validateUsageBreakdownRow(raw.other, "other"),
    total: validateUsageBreakdownTotal(raw.total, "total"),
  };
}
