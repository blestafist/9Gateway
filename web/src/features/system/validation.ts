import { ValidationError } from "../../shared/transport";
import {
  ReadinessCheck,
  ReadinessResponse,
  ReadinessSummary,
  StorageSummary,
  TelemetrySummary,
  SystemLimits,
  SystemResponse,
} from "./types";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function validateReadinessCheck(name: string, raw: unknown): ReadinessCheck {
  if (!isObject(raw)) {
    throw new ValidationError(`Readiness check ${name} must be an object`);
  }

  if (typeof raw.name !== "string") {
    throw new ValidationError(`Readiness check ${name} name must be a string`);
  }

  if (typeof raw.status !== "string") {
    throw new ValidationError(`Readiness check ${name} status must be a string`);
  }

  if (raw.message !== undefined && raw.message !== null && typeof raw.message !== "string") {
    throw new ValidationError(`Readiness check ${name} message must be a string or undefined`);
  }

  return {
    name: raw.name,
    status: raw.status as ReadinessCheck["status"],
    message: typeof raw.message === "string" ? raw.message : undefined,
  };
}

export function validateReadinessSummary(raw: unknown): ReadinessSummary {
  if (!isObject(raw)) {
    throw new ValidationError("Readiness summary must be an object");
  }

  if (typeof raw.ready !== "boolean") {
    throw new ValidationError("Readiness summary ready field must be a boolean");
  }

  if (!isObject(raw.checks)) {
    throw new ValidationError("Readiness summary checks must be an object");
  }

  const checks: Record<string, ReadinessCheck> = {};
  for (const [key, value] of Object.entries(raw.checks)) {
    checks[key] = validateReadinessCheck(key, value);
  }

  return {
    ready: raw.ready,
    checks,
  };
}

export function validateStorageSummary(raw: unknown, label: string = "storage"): StorageSummary {
  if (!isObject(raw)) {
    throw new ValidationError(`${label} summary must be an object`);
  }

  if (typeof raw.status !== "string") {
    throw new ValidationError(`${label} status must be a string`);
  }

  if (typeof raw.healthy !== "boolean") {
    throw new ValidationError(`${label} healthy must be a boolean`);
  }

  if (
    raw.schema_version !== null &&
    raw.schema_version !== undefined &&
    typeof raw.schema_version !== "number"
  ) {
    throw new ValidationError(`${label} schema_version must be a number or null`);
  }

  if (typeof raw.current_schema_version !== "number") {
    throw new ValidationError(`${label} current_schema_version must be a number`);
  }

  return {
    status: raw.status,
    healthy: raw.healthy,
    schema_version: typeof raw.schema_version === "number" ? raw.schema_version : null,
    current_schema_version: raw.current_schema_version,
  };
}

export function validateTelemetrySummary(raw: unknown): TelemetrySummary {
  if (!isObject(raw)) {
    throw new ValidationError("Telemetry summary must be an object");
  }

  if (typeof raw.queue_depth !== "number") {
    throw new ValidationError("Telemetry queue_depth must be a number");
  }

  if (typeof raw.queue_capacity !== "number") {
    throw new ValidationError("Telemetry queue_capacity must be a number");
  }

  if (typeof raw.dropped_records !== "number") {
    throw new ValidationError("Telemetry dropped_records must be a number");
  }

  return {
    queue_depth: raw.queue_depth,
    queue_capacity: raw.queue_capacity,
    dropped_records: raw.dropped_records,
  };
}

export function validateSystemLimits(raw: unknown): SystemLimits {
  if (!isObject(raw)) {
    throw new ValidationError("Limits summary must be an object");
  }

  if (typeof raw.request_retention_seconds !== "number") {
    throw new ValidationError("Limits request_retention_seconds must be a number");
  }

  if (typeof raw.body_retention_seconds !== "number") {
    throw new ValidationError("Limits body_retention_seconds must be a number");
  }

  if (typeof raw.max_captured_body_bytes !== "number") {
    throw new ValidationError("Limits max_captured_body_bytes must be a number");
  }

  return {
    request_retention_seconds: raw.request_retention_seconds,
    body_retention_seconds: raw.body_retention_seconds,
    max_captured_body_bytes: raw.max_captured_body_bytes,
  };
}

export function validateSystemResponse(raw: unknown): SystemResponse {
  if (!isObject(raw)) {
    throw new ValidationError("System response must be an object");
  }

  if (typeof raw.version !== "string") {
    throw new ValidationError("System response version must be a string");
  }

  if (typeof raw.commit !== "string") {
    throw new ValidationError("System response commit must be a string");
  }

  if (typeof raw.build_time !== "string") {
    throw new ValidationError("System response build_time must be a string");
  }

  if (typeof raw.build_date !== "string") {
    throw new ValidationError("System response build_date must be a string");
  }

  if (typeof raw.start_time !== "string") {
    throw new ValidationError("System response start_time must be a string");
  }

  if (typeof raw.uptime_seconds !== "number") {
    throw new ValidationError("System response uptime_seconds must be a number");
  }

  if (typeof raw.ready !== "boolean") {
    throw new ValidationError("System response ready must be a boolean");
  }

  if (typeof raw.active_requests !== "number") {
    throw new ValidationError("System response active_requests must be a number");
  }

  const readiness = validateReadinessSummary(raw.readiness);
  const storage = validateStorageSummary(raw.storage, "storage");
  const sqlite = validateStorageSummary(raw.sqlite ?? raw.storage, "sqlite");
  const telemetry = validateTelemetrySummary(raw.telemetry);
  const limits = validateSystemLimits(raw.limits);

  return {
    version: raw.version,
    commit: raw.commit,
    build_time: raw.build_time,
    build_date: raw.build_date,
    start_time: raw.start_time,
    uptime_seconds: raw.uptime_seconds,
    ready: raw.ready,
    readiness,
    storage,
    sqlite,
    telemetry,
    active_requests: raw.active_requests,
    limits,
  };
}

export function validateReadinessResponse(raw: unknown): ReadinessResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Readiness response must be an object");
  }

  if (typeof raw.ready !== "boolean") {
    throw new ValidationError("Readiness response ready field must be a boolean");
  }

  if (!isObject(raw.checks)) {
    throw new ValidationError("Readiness response checks must be an object");
  }

  const checks: Record<string, ReadinessCheck> = {};
  for (const [key, value] of Object.entries(raw.checks)) {
    checks[key] = validateReadinessCheck(key, value);
  }

  if (typeof raw.version !== "string") {
    throw new ValidationError("Readiness response version must be a string");
  }

  if (typeof raw.commit !== "string") {
    throw new ValidationError("Readiness response commit must be a string");
  }

  return {
    ready: raw.ready,
    checks,
    version: raw.version,
    commit: raw.commit,
  };
}
