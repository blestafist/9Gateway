import { ValidationError } from "../../shared/transport";
import { parseDurationToSeconds } from "./policyHelpers";
import {
  AdminKeyDetail,
  AdminKeyListItem,
  AdminKeyListResponse,
  AdminKeyPolicy,
  CreateAdminKeyResponse,
  KeyPolicySummary,
} from "./types";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

const MAX_INT64 = 9223372036854775807n;
function validateInt64Value(value: unknown, label: string): number | string {
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new ValidationError(`${label} must be a safe non-negative integer or decimal string`);
    }
    return value;
  }
  if (typeof value !== "string" || !/^\d+$/.test(value)) {
    throw new ValidationError(`${label} must be a safe non-negative integer or decimal string`);
  }
  try {
    const parsed = BigInt(value);
    if (parsed < 0n || parsed > MAX_INT64) throw new Error("out of range");
    return parsed <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(parsed) : parsed.toString();
  } catch {
    throw new ValidationError(`${label} must be a safe non-negative integer or decimal string`);
  }
}

export function validateKeyPolicySummary(raw: unknown): KeyPolicySummary {
  if (!isObject(raw)) {
    throw new ValidationError("Key policy summary must be an object");
  }
  if (typeof raw.allow_models !== "boolean") {
    throw new ValidationError("Key policy summary allow_models must be a boolean");
  }
  if (typeof raw.deny_models !== "boolean") {
    throw new ValidationError("Key policy summary deny_models must be a boolean");
  }
  if (typeof raw.log_request_body !== "boolean") {
    throw new ValidationError("Key policy summary log_request_body must be a boolean");
  }
  if (typeof raw.log_response_body !== "boolean") {
    throw new ValidationError("Key policy summary log_response_body must be a boolean");
  }
  return {
    allow_models: raw.allow_models,
    deny_models: raw.deny_models,
    log_request_body: raw.log_request_body,
    log_response_body: raw.log_response_body,
  };
}

export function validateKeyListItem(raw: unknown): AdminKeyListItem {
  if (!isObject(raw)) {
    throw new ValidationError("Key list item must be an object");
  }

  if (typeof raw.id !== "string" || !raw.id) {
    throw new ValidationError("Key id must be a non-empty string");
  }

  if (typeof raw.name !== "string") {
    throw new ValidationError("Key name must be a string");
  }

  if (typeof raw.display_prefix !== "string") {
    throw new ValidationError("Key display_prefix must be a string");
  }

  if (typeof raw.enabled !== "boolean") {
    throw new ValidationError("Key enabled must be a boolean");
  }

  if (typeof raw.created_at !== "string") {
    throw new ValidationError("Key created_at must be a string");
  }

  if (typeof raw.updated_at !== "string") {
    throw new ValidationError("Key updated_at must be a string");
  }

  if (raw.expires_at !== null && raw.expires_at !== undefined && typeof raw.expires_at !== "string") {
    throw new ValidationError("Key expires_at must be a string, null, or undefined");
  }

  return {
    id: raw.id,
    name: raw.name,
    display_prefix: raw.display_prefix,
    enabled: raw.enabled,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : null,
    policy_summary: validateKeyPolicySummary(raw.policy_summary),
  };
}

export function validateKeyListResponse(raw: unknown): AdminKeyListResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Key list response must be an object");
  }

  if (!Array.isArray(raw.keys)) {
    throw new ValidationError("Key list response keys must be an array");
  }

  const keys = raw.keys.map((k) => validateKeyListItem(k));

  if (raw.next_cursor !== undefined && raw.next_cursor !== null && typeof raw.next_cursor !== "string") {
    throw new ValidationError("Key list next_cursor must be a string or undefined");
  }

  return {
    keys,
    next_cursor: typeof raw.next_cursor === "string" ? raw.next_cursor : undefined,
  };
}

export function validateKeyPolicy(raw: unknown): AdminKeyPolicy {
  if (!isObject(raw)) {
    throw new ValidationError("Key policy must be an object");
  }

  if (raw.allowed_models !== undefined) {
    if (!Array.isArray(raw.allowed_models) || !raw.allowed_models.every((m) => typeof m === "string")) {
      throw new ValidationError("Key policy allowed_models must be an array of strings");
    }
  }

  if (raw.denied_models !== undefined) {
    if (!Array.isArray(raw.denied_models) || !raw.denied_models.every((m) => typeof m === "string")) {
      throw new ValidationError("Key policy denied_models must be an array of strings");
    }
  }

  let normalizedRequestWindows: { amount: number; duration: number }[] = [];
  if (raw.request_windows !== undefined) {
    if (!Array.isArray(raw.request_windows)) {
      throw new ValidationError("Key policy request_windows must be an array");
    }
    normalizedRequestWindows = raw.request_windows.map((w) => {
      if (!isObject(w)) {
        throw new ValidationError("Key policy request_windows item must be an object");
      }
      if (typeof w.amount !== "number" || Number.isNaN(w.amount)) {
        throw new ValidationError("Key policy request_windows amount must be a number");
      }
      const durationSec = parseDurationToSeconds(w.duration as string | number);
      return {
        amount: w.amount,
        duration: durationSec,
      };
    });
  }

  let normalizedTokenWindows: { amount: number | string; duration: number }[] = [];
  if (raw.token_windows !== undefined) {
    if (!Array.isArray(raw.token_windows)) {
      throw new ValidationError("Key policy token_windows must be an array");
    }
    normalizedTokenWindows = raw.token_windows.map((w) => {
      if (!isObject(w)) {
        throw new ValidationError("Key policy token_windows item must be an object");
      }
       const amount = validateInt64Value(w.amount, "Key policy token_windows amount");
       const durationSec = parseDurationToSeconds(w.duration as string | number);
       return {
         amount,
         duration: durationSec,
       };
    });
  }

  if (raw.token_mode !== undefined && typeof raw.token_mode !== "string") {
    throw new ValidationError("Key policy token_mode must be a string");
  }

  if (
    raw.max_concurrent_requests !== undefined &&
    (typeof raw.max_concurrent_requests !== "number" || Number.isNaN(raw.max_concurrent_requests))
  ) {
    throw new ValidationError("Key policy max_concurrent_requests must be a number");
  }

  let normalizedBudgetLimits: { period: string; amount_micros: number | string }[] = [];
  if (raw.budget_limits !== undefined) {
    if (!Array.isArray(raw.budget_limits)) {
      throw new ValidationError("Key policy budget_limits must be an array");
    }
    normalizedBudgetLimits = raw.budget_limits.map((b) => {
      if (!isObject(b)) {
        throw new ValidationError("Key policy budget_limits item must be an object");
      }
      if (typeof b.period !== "string") {
        throw new ValidationError("Key policy budget_limits period must be a string");
      }
      return { period: b.period, amount_micros: validateInt64Value(b.amount_micros, "Key policy budget_limits amount_micros") };
    });
  }

  if (typeof raw.log_request_body !== "boolean") {
    throw new ValidationError("Key policy log_request_body must be a boolean");
  }

  if (typeof raw.log_response_body !== "boolean") {
    throw new ValidationError("Key policy log_response_body must be a boolean");
  }

  return {
    allowed_models: (raw.allowed_models as string[]) || [],
    denied_models: (raw.denied_models as string[]) || [],
    request_windows: normalizedRequestWindows,
    token_windows: normalizedTokenWindows,
    token_mode: typeof raw.token_mode === "string" ? raw.token_mode : "total",
    max_concurrent_requests: typeof raw.max_concurrent_requests === "number" ? raw.max_concurrent_requests : 0,
    budget_limits: normalizedBudgetLimits,
    log_request_body: raw.log_request_body,
    log_response_body: raw.log_response_body,
  };
}

export function validateKeyDetail(raw: unknown): AdminKeyDetail {
  if (!isObject(raw)) {
    throw new ValidationError("Key detail must be an object");
  }

  if (typeof raw.id !== "string" || !raw.id) {
    throw new ValidationError("Key id must be a non-empty string");
  }

  if (typeof raw.name !== "string") {
    throw new ValidationError("Key name must be a string");
  }

  if (typeof raw.display_prefix !== "string") {
    throw new ValidationError("Key display_prefix must be a string");
  }

  if (typeof raw.enabled !== "boolean") {
    throw new ValidationError("Key enabled must be a boolean");
  }

  if (typeof raw.created_at !== "string") {
    throw new ValidationError("Key created_at must be a string");
  }

  if (typeof raw.updated_at !== "string") {
    throw new ValidationError("Key updated_at must be a string");
  }

  if (raw.expires_at !== null && raw.expires_at !== undefined && typeof raw.expires_at !== "string") {
    throw new ValidationError("Key expires_at must be a string, null, or undefined");
  }

  if (!isObject(raw.policy)) {
    throw new ValidationError("Key policy must be an object");
  }

  return {
    id: raw.id,
    name: raw.name,
    display_prefix: raw.display_prefix,
    enabled: raw.enabled,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : null,
    policy: validateKeyPolicy(raw.policy),
  };
}

export function validateCreateKeyResponse(raw: unknown): CreateAdminKeyResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Create key response must be an object");
  }

  if (typeof raw.id !== "string" || !raw.id) {
    throw new ValidationError("Create key response missing or empty id");
  }

  if (typeof raw.key !== "string" || !raw.key) {
    throw new ValidationError("Create key response missing or empty key");
  }

  if (typeof raw.name !== "string") {
    throw new ValidationError("Create key response name must be a string");
  }

  if (typeof raw.prefix !== "string") {
    throw new ValidationError("Create key response prefix must be a string");
  }

  if (typeof raw.enabled !== "boolean") {
    throw new ValidationError("Create key response enabled must be a boolean");
  }

  if (typeof raw.created_at !== "string") {
    throw new ValidationError("Create key response created_at must be a string");
  }

  if (raw.expires_at !== undefined && raw.expires_at !== null && typeof raw.expires_at !== "string") {
    throw new ValidationError("Create key response expires_at must be a string or undefined");
  }

  if (!isObject(raw.policy)) {
    throw new ValidationError("Create key response policy must be an object");
  }

  return {
    id: raw.id,
    name: raw.name,
    prefix: raw.prefix,
    enabled: raw.enabled,
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : undefined,
    created_at: raw.created_at,
    key: raw.key,
    policy: raw.policy,
  };
}
