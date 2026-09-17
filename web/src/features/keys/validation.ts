import { ValidationError } from "../../shared/transport";
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

export function validateKeyPolicySummary(raw: unknown): KeyPolicySummary {
  if (!isObject(raw)) {
    throw new ValidationError("Key policy summary must be an object");
  }
  return {
    allow_models: Boolean(raw.allow_models),
    deny_models: Boolean(raw.deny_models),
    log_request_body: Boolean(raw.log_request_body),
    log_response_body: Boolean(raw.log_response_body),
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

  const expires_at =
    raw.expires_at === null || typeof raw.expires_at === "string"
      ? raw.expires_at
      : null;

  return {
    id: raw.id,
    name: raw.name,
    display_prefix: raw.display_prefix,
    enabled: raw.enabled,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
    expires_at,
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
  const next_cursor =
    typeof raw.next_cursor === "string" ? raw.next_cursor : undefined;

  return {
    keys,
    next_cursor,
  };
}

export function validateKeyPolicy(raw: unknown): AdminKeyPolicy {
  if (!isObject(raw)) {
    throw new ValidationError("Key policy must be an object");
  }

  return {
    allowed_models: Array.isArray(raw.allowed_models)
      ? raw.allowed_models.filter((m): m is string => typeof m === "string")
      : [],
    denied_models: Array.isArray(raw.denied_models)
      ? raw.denied_models.filter((m): m is string => typeof m === "string")
      : [],
    request_windows: Array.isArray(raw.request_windows)
      ? raw.request_windows
          .filter(isObject)
          .map((w) => ({ amount: Number(w.amount) || 0, duration: Number(w.duration) || 0 }))
      : [],
    token_windows: Array.isArray(raw.token_windows)
      ? raw.token_windows
          .filter(isObject)
          .map((w) => ({ amount: Number(w.amount) || 0, duration: Number(w.duration) || 0 }))
      : [],
    token_mode: typeof raw.token_mode === "string" ? raw.token_mode : "total",
    max_concurrent_requests: Number(raw.max_concurrent_requests) || 0,
    budget_limits: Array.isArray(raw.budget_limits)
      ? raw.budget_limits
          .filter(isObject)
          .map((b) => ({ period: String(b.period || ""), amount_micros: Number(b.amount_micros) || 0 }))
      : [],
    log_request_body: Boolean(raw.log_request_body),
    log_response_body: Boolean(raw.log_response_body),
  };
}

export function validateKeyDetail(raw: unknown): AdminKeyDetail {
  if (!isObject(raw)) {
    throw new ValidationError("Key detail must be an object");
  }

  if (typeof raw.id !== "string" || !raw.id) {
    throw new ValidationError("Key id must be a non-empty string");
  }

  return {
    id: raw.id,
    name: typeof raw.name === "string" ? raw.name : "",
    display_prefix: typeof raw.display_prefix === "string" ? raw.display_prefix : "",
    enabled: Boolean(raw.enabled),
    created_at: typeof raw.created_at === "string" ? raw.created_at : "",
    updated_at: typeof raw.updated_at === "string" ? raw.updated_at : "",
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : null,
    policy: validateKeyPolicy(raw.policy),
  };
}

export function validateCreateKeyResponse(raw: unknown): CreateAdminKeyResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Create key response must be an object");
  }

  if (typeof raw.id !== "string" || typeof raw.key !== "string") {
    throw new ValidationError("Create key response missing id or raw key");
  }

  return {
    id: raw.id,
    name: typeof raw.name === "string" ? raw.name : "",
    prefix: typeof raw.prefix === "string" ? raw.prefix : "",
    enabled: Boolean(raw.enabled),
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : undefined,
    created_at: typeof raw.created_at === "string" ? raw.created_at : "",
    key: raw.key,
    policy: isObject(raw.policy) ? raw.policy : {},
  };
}
