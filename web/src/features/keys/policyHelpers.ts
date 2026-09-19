import { ValidationError } from "../../shared/transport";
import { AdminKeyDetail, UpdateAdminKeyPolicyRequest } from "./types";

export type DurationUnit = "s" | "m" | "h" | "d";
export type BudgetUnit = "usd" | "micros";

export interface RequestWindowInput {
  id: string;
  amount: number | "";
  durationValue: number | "";
  durationUnit: DurationUnit;
}

export interface TokenWindowInput {
  id: string;
  /** Keep the token count textual while editing so unsafe integers are never rounded. */
  amount: number | string;
  durationValue: number | "";
  durationUnit: DurationUnit;
}

export interface BudgetLimitInput {
  id: string;
  period: "total" | "day" | "month";
  amount: string;
  unit: BudgetUnit;
}

export interface KeyPolicyFormValues {
  enabled: boolean;
  allowed_models: string[];
  denied_models: string[];
  request_windows: RequestWindowInput[];
  token_windows: TokenWindowInput[];
  token_mode: "estimate" | "usage_only" | string;
  concurrency_mode: "unlimited" | "custom";
  max_concurrent_requests: number | "";
  budget_limits: BudgetLimitInput[];
  log_request_body: boolean;
  log_response_body: boolean;
}

export interface PolicyValidationErrors {
  summary: string[];
  fieldErrors: Record<string, string>;
  firstErrorFieldId?: string;
}

export interface SecuritySensitiveChange {
  id: string;
  severity: "high" | "medium";
  label: string;
  description: string;
}

/**
 * Parses a Go duration string (e.g. "60s", "1m", "2h", "1d") or number of seconds into whole seconds.
 */
export function parseDurationToSeconds(duration: string | number): number {
  if (typeof duration === "number") {
    if (Number.isNaN(duration) || duration < 0) {
      throw new ValidationError(`Invalid numeric duration: ${duration}`);
    }
    return Math.floor(duration);
  }
  if (typeof duration !== "string") {
    throw new ValidationError(`Invalid duration type: ${typeof duration}`);
  }
  const trimmed = duration.trim();
  const match = trimmed.match(/^(\d+)(s|m|h|d)$/);
  if (!match) {
    throw new ValidationError(`Cannot parse duration string: ${duration}`);
  }
  const val = parseInt(match[1]!, 10);
  const unit = match[2] as DurationUnit;
  switch (unit) {
    case "d":
      return val * 86400;
    case "h":
      return val * 3600;
    case "m":
      return val * 60;
    case "s":
    default:
      return val;
  }
}

/**
 * Decomposes integer seconds into a convenient amount + unit representation.
 */
export function secondsToDurationInput(seconds: number): { amount: number; unit: DurationUnit } {
  if (seconds <= 0) {
    return { amount: 0, unit: "s" };
  }
  if (seconds % 86400 === 0) {
    return { amount: seconds / 86400, unit: "d" };
  }
  if (seconds % 3600 === 0) {
    return { amount: seconds / 3600, unit: "h" };
  }
  if (seconds % 60 === 0) {
    return { amount: seconds / 60, unit: "m" };
  }
  return { amount: seconds, unit: "s" };
}

/**
 * Multiplies an amount and unit into exact integer seconds.
 */
export function durationInputToSeconds(amount: number, unit: DurationUnit): number {
  switch (unit) {
    case "d":
      return amount * 86400;
    case "h":
      return amount * 3600;
    case "m":
      return amount * 60;
    case "s":
    default:
      return amount;
  }
}

/**
 * Converts micro-dollars (1 USD = 1,000,000 µ$) to a decimal dollar string without precision loss.
 */
const MAX_INT64 = 9223372036854775807n;
const MAX_SAFE_INTEGER_BIGINT = BigInt(Number.MAX_SAFE_INTEGER);
export type IntegerValue = number | string;
export function formatInteger(value: IntegerValue): string {
  const normalized = integerString(value);
  return normalized === null ? String(value) : BigInt(normalized).toLocaleString();
}

function integerString(value: IntegerValue): string | null {
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) return null;
    return String(value);
  }
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  try {
    const parsed = BigInt(trimmed);
    if (parsed < 0n || parsed > MAX_INT64) return null;
    return parsed.toString();
  } catch {
    return null;
  }
}

function integerForPayload(value: IntegerValue): number {
  const normalized = integerString(value);
  if (normalized === null) throw new ValidationError("Integer value must be a non-negative safe integer or int64 decimal string");
  const parsed = BigInt(normalized);
  if (parsed > MAX_SAFE_INTEGER_BIGINT) {
    throw new ValidationError("Unsafe int64 values cannot be submitted through the numeric JSON policy contract");
  }
  return Number(parsed);
}

export function isUnsupportedInt64(value: IntegerValue): boolean {
  if (typeof value === "number") {
    return !Number.isSafeInteger(value);
  }
  const normalized = integerString(value);
  if (normalized === null) return false;
  try {
    const parsed = BigInt(normalized);
    return parsed > MAX_SAFE_INTEGER_BIGINT;
  } catch {
    return false;
  }
}

export function isBudgetLimitUnsupported(limit: BudgetLimitInput): boolean {
  if (!limit.amount || limit.amount.trim() === "") return false;
  if (limit.unit === "usd") {
    const micros = dollarsStringToMicros(limit.amount);
    if (micros === null) return false;
    if (typeof micros === "string") {
      try {
        return BigInt(micros) > MAX_SAFE_INTEGER_BIGINT;
      } catch {
        return false;
      }
    }
    return !Number.isSafeInteger(micros);
  } else {
    return isUnsupportedInt64(limit.amount);
  }
}

export function hasUnsupportedNumericPolicyValues(values: KeyPolicyFormValues): boolean {
  const hasTokenUnsafe = values.token_windows.some((w) => isUnsupportedInt64(w.amount));
  const hasBudgetUnsafe = values.budget_limits.some((b) => isBudgetLimitUnsupported(b));
  const hasReqUnsafe = values.request_windows.some(
    (w) => typeof w.amount === "number" && !Number.isSafeInteger(w.amount)
  );
  return hasTokenUnsafe || hasBudgetUnsafe || hasReqUnsafe;
}

/** Converts int64 micro-dollars to a decimal dollar string without precision loss. */
export function microsToDollarsString(micros: IntegerValue): string {
  const normalized = integerString(micros);
  if (normalized === null) return "0";
  const value = BigInt(normalized);
  if (value === 0n) return "0";
  const whole = value / 1_000_000n;
  const remainder = value % 1_000_000n;
  if (remainder === 0n) return whole.toString();
  return `${whole}.${remainder.toString().padStart(6, "0").replace(/0+$/, "")}`;
}

/**
 * Converts a decimal dollar string (up to 6 decimal digits) to integer micro-dollars.
 * Safe values retain the historical number return type; larger int64 values are strings.
 */
export function dollarsStringToMicros(dollarsStr: string): number | string | null {
  const trimmed = dollarsStr.trim().replace(/^\$/, "");
  if (!trimmed || !/^\d+(\.\d{1,6})?$/.test(trimmed)) return null;
  const parts = trimmed.split(".");
  const whole = parts[0] || "0";
  const decimal = (parts[1] || "").padEnd(6, "0");
  try {
    const micros = BigInt(whole) * 1_000_000n + BigInt(decimal || "0");
    if (micros < 0n || micros > MAX_INT64) return null;
    return micros <= MAX_SAFE_INTEGER_BIGINT ? Number(micros) : micros.toString();
  } catch {
    return null;
  }
}

/**
 * Reorders an item in a list up or down immutably.
 */
export function reorderItem<T>(list: T[], index: number, direction: "up" | "down"): T[] {
  if (direction === "up" && index <= 0) return list;
  if (direction === "down" && index >= list.length - 1) return list;
  const targetIndex = direction === "up" ? index - 1 : index + 1;
  const copy = [...list];
  const item = copy[index];
  const target = copy[targetIndex];
  if (item === undefined || target === undefined) return list;
  copy[index] = target;
  copy[targetIndex] = item;
  return copy;
}

let nextId = 1;
export function generateRowId(): string {
  return `row-${Date.now()}-${nextId++}`;
}

/**
 * Maps AdminKeyDetail to KeyPolicyFormValues for initial form state.
 */
export function policyToFormValues(detail: AdminKeyDetail): KeyPolicyFormValues {
  const policy = detail.policy;

  const requestWindows: RequestWindowInput[] = (policy.request_windows || []).map((w) => {
    const sec = typeof w.duration === "number" ? w.duration : parseDurationToSeconds(w.duration);
    const decomposed = secondsToDurationInput(sec);
    return {
      id: generateRowId(),
      amount: w.amount,
      durationValue: decomposed.amount,
      durationUnit: decomposed.unit,
    };
  });

  const tokenWindows: TokenWindowInput[] = (policy.token_windows || []).map((w) => {
    const normalizedAmount = integerString(w.amount);
    const sec = typeof w.duration === "number" ? w.duration : parseDurationToSeconds(w.duration);
    const decomposed = secondsToDurationInput(sec);
    return {
      id: generateRowId(),
      // Preserve an unsafe numeric response unchanged so validation can reject
      // it; never coerce it through a rounded representation.
      amount: normalizedAmount ?? w.amount,
      durationValue: decomposed.amount,
      durationUnit: decomposed.unit,
    };
  });

  const budgetLimits: BudgetLimitInput[] = (policy.budget_limits || []).map((b) => {
    let period: "total" | "day" | "month" = "total";
    if (b.period === "day" || b.period === "daily") period = "day";
    else if (b.period === "month" || b.period === "monthly") period = "month";
    else period = "total";

    const normalizedMicros = integerString(b.amount_micros);
    if (normalizedMicros === null) throw new ValidationError("Policy budget amount is outside the supported int64 range");
    const isTinyMicros = BigInt(normalizedMicros) < 1000n && BigInt(normalizedMicros) > 0n;

    return {
      id: generateRowId(),
      period,
      amount: isTinyMicros ? normalizedMicros : microsToDollarsString(normalizedMicros),
      unit: isTinyMicros ? "micros" : "usd",
    };
  });

  const concurrency = policy.max_concurrent_requests ?? 0;

  return {
    enabled: detail.enabled,
    allowed_models: [...(policy.allowed_models || [])],
    denied_models: [...(policy.denied_models || [])],
    request_windows: requestWindows,
    token_windows: tokenWindows,
    token_mode: policy.token_mode === "usage_only" ? "usage_only" : "estimate",
    concurrency_mode: concurrency > 0 ? "custom" : "unlimited",
    max_concurrent_requests: concurrency > 0 ? concurrency : "",
    budget_limits: budgetLimits,
    log_request_body: Boolean(policy.log_request_body),
    log_response_body: Boolean(policy.log_response_body),
  };
}

/**
 * Validates KeyPolicyFormValues and returns errors.
 */
export function validatePolicyForm(values: KeyPolicyFormValues): PolicyValidationErrors {
  const summary: string[] = [];
  const fieldErrors: Record<string, string> = {};
  let firstErrorFieldId: string | undefined;

  const recordError = (fieldId: string, message: string) => {
    fieldErrors[fieldId] = message;
    summary.push(message);
    if (!firstErrorFieldId) {
      firstErrorFieldId = fieldId;
    }
  };

  // Allowed models
  const seenAllowed = new Set<string>();
  values.allowed_models.forEach((pattern, index) => {
    const fieldId = `allowed-model-${index}`;
    const trimmed = pattern.trim();
    if (!trimmed) {
      recordError(fieldId, `Allowed model pattern #${index + 1} cannot be empty`);
    } else if (seenAllowed.has(trimmed)) {
      recordError(fieldId, `Duplicate allowed model pattern: "${trimmed}"`);
    } else {
      seenAllowed.add(trimmed);
    }
  });

  // Denied models
  const seenDenied = new Set<string>();
  values.denied_models.forEach((pattern, index) => {
    const fieldId = `denied-model-${index}`;
    const trimmed = pattern.trim();
    if (!trimmed) {
      recordError(fieldId, `Denied model pattern #${index + 1} cannot be empty`);
    } else if (seenDenied.has(trimmed)) {
      recordError(fieldId, `Duplicate denied model pattern: "${trimmed}"`);
    } else {
      seenDenied.add(trimmed);
    }
  });

  // Request windows
  const seenRequestDurations = new Set<number>();
  values.request_windows.forEach((w, index) => {
    const amountField = `req-window-amount-${index}`;
    const durationField = `req-window-duration-${index}`;

    if (w.amount === "" || typeof w.amount !== "number" || Number.isNaN(w.amount) || w.amount <= 0 || !Number.isInteger(w.amount)) {
      recordError(amountField, `Request limit #${index + 1} amount must be an integer greater than 0`);
    }

    if (w.durationValue === "" || typeof w.durationValue !== "number" || Number.isNaN(w.durationValue) || w.durationValue <= 0 || !Number.isInteger(w.durationValue)) {
      recordError(durationField, `Request limit #${index + 1} duration must be an integer greater than 0`);
    } else {
      const totalSec = durationInputToSeconds(w.durationValue, w.durationUnit);
      if (seenRequestDurations.has(totalSec)) {
        recordError(durationField, `Duplicate request window duration (${totalSec}s) at row #${index + 1}`);
      } else {
        seenRequestDurations.add(totalSec);
      }
    }
  });

  // Token windows
  const seenTokenDurations = new Set<number>();
  values.token_windows.forEach((w, index) => {
    const amountField = `tok-window-amount-${index}`;
    const durationField = `tok-window-duration-${index}`;

    const tokenAmount = w.amount === "" ? null : integerString(w.amount);
    if (tokenAmount === null || tokenAmount === "0") {
      recordError(amountField, `Token limit #${index + 1} amount must be an integer greater than 0 and no greater than int64 max`);
    }

    if (w.durationValue === "" || typeof w.durationValue !== "number" || Number.isNaN(w.durationValue) || w.durationValue <= 0 || !Number.isInteger(w.durationValue)) {
      recordError(durationField, `Token limit #${index + 1} duration must be an integer greater than 0`);
    } else {
      const totalSec = durationInputToSeconds(w.durationValue, w.durationUnit);
      if (seenTokenDurations.has(totalSec)) {
        recordError(durationField, `Duplicate token window duration (${totalSec}s) at row #${index + 1}`);
      } else {
        seenTokenDurations.add(totalSec);
      }
    }
  });

  // Concurrency
  if (values.concurrency_mode === "custom") {
    const concurrencyField = "max-concurrency-input";
    if (
      values.max_concurrent_requests === "" ||
      typeof values.max_concurrent_requests !== "number" ||
      Number.isNaN(values.max_concurrent_requests) ||
      values.max_concurrent_requests <= 0 ||
      !Number.isInteger(values.max_concurrent_requests)
    ) {
      recordError(concurrencyField, "Max concurrent requests must be an integer greater than 0 (or select Unlimited)");
    }
  }

  // Budget limits
  const seenBudgetPeriods = new Set<string>();
  values.budget_limits.forEach((b, index) => {
    const periodField = `budget-period-${index}`;
    const amountField = `budget-amount-${index}`;

    if (seenBudgetPeriods.has(b.period)) {
      recordError(periodField, `Duplicate budget limit period: "${b.period}" at row #${index + 1}`);
    } else {
      seenBudgetPeriods.add(b.period);
    }

    if (!b.amount || b.amount.trim() === "") {
      recordError(amountField, `Budget amount #${index + 1} cannot be empty`);
    } else if (b.unit === "usd") {
      const micros = dollarsStringToMicros(b.amount);
      const isNonPositive = typeof micros === "number" ? micros <= 0 : micros === "0";
      if (micros === null || isNonPositive) {
        recordError(amountField, `Budget amount #${index + 1} must be a valid dollar amount greater than $0 (max 6 decimal places)`);
      }
    } else {
      const num = integerString(b.amount);
      if (num === null || num === "0") {
        recordError(amountField, `Budget amount #${index + 1} must be an integer micro-dollar amount greater than 0 and no greater than int64 max`);
      }
    }
  });

  return {
    summary,
    fieldErrors,
    firstErrorFieldId,
  };
}

/**
 * Converts KeyPolicyFormValues to the PUT /admin/v1/keys/:id/policy request payload.
 */
export function formValuesToPolicyPayload(values: KeyPolicyFormValues): UpdateAdminKeyPolicyRequest {
  const requestWindows = values.request_windows.map((w) => {
    const sec = durationInputToSeconds(Number(w.durationValue), w.durationUnit);
    return {
      amount: Number(w.amount),
      duration: `${sec}s`,
    };
  });

  const tokenWindows = values.token_windows.map((w) => {
    const sec = durationInputToSeconds(Number(w.durationValue), w.durationUnit);
    return {
      amount: integerForPayload(w.amount),
      duration: `${sec}s`,
    };
  });

  const budgetLimits = values.budget_limits.map((b) => {
    const micros = b.unit === "usd" ? dollarsStringToMicros(b.amount) : integerString(b.amount);
    if (micros === null) throw new ValidationError("Budget amount is outside the supported int64 range");
    return {
      period: b.period,
      amount_micros: integerForPayload(micros),
    };
  });

  const maxConcurrency = values.concurrency_mode === "unlimited" ? 0 : Number(values.max_concurrent_requests);

  return {
    enabled: values.enabled,
    policy: {
      allowed_models: values.allowed_models,
      denied_models: values.denied_models,
      request_windows: requestWindows,
      token_windows: tokenWindows,
      token_mode: values.token_mode,
      max_concurrent_requests: maxConcurrency,
      budget_limits: budgetLimits,
      log_request_body: values.log_request_body,
      log_response_body: values.log_response_body,
    },
  };
}

/**
 * Safely serializes policy form values to a JSON payload string.
 * Returns null if the values cannot be represented by the numeric JSON contract.
 */
export function serializePolicyPayloadSafe(values: KeyPolicyFormValues): string | null {
  try {
    return JSON.stringify(formValuesToPolicyPayload(values));
  } catch {
    return null;
  }
}

/**
 * Produces a stable serialized fingerprint of user-editable form values.
 * Used for dirty checking even when values are outside the numeric contract.
 */
export function getFormValuesFingerprint(v: KeyPolicyFormValues): string {
  return JSON.stringify({
    enabled: v.enabled,
    allowed_models: v.allowed_models,
    denied_models: v.denied_models,
    request_windows: v.request_windows.map((w) => ({
      amount: w.amount,
      durVal: w.durationValue,
      durUnit: w.durationUnit,
    })),
    token_windows: v.token_windows.map((w) => ({
      amount: String(w.amount),
      durVal: w.durationValue,
      durUnit: w.durationUnit,
    })),
    token_mode: v.token_mode,
    concurrency_mode: v.concurrency_mode,
    max_concurrent_requests: v.max_concurrent_requests,
    budget_limits: v.budget_limits.map((b) => ({
      period: b.period,
      amount: b.amount,
      unit: b.unit,
    })),
    log_request_body: v.log_request_body,
    log_response_body: v.log_response_body,
  });
}

/**
 * Detects security-sensitive differences between the server record and the current form values.
 */
export function detectSecuritySensitiveChanges(
  original: AdminKeyDetail,
  current: KeyPolicyFormValues
): SecuritySensitiveChange[] {
  const changes: SecuritySensitiveChange[] = [];

  // Enabled state changed
  if (original.enabled && !current.enabled) {
    changes.push({
      id: "disabled",
      severity: "high",
      label: "Key Will Be Disabled",
      description: "All incoming requests authenticated with this key will be rejected immediately with HTTP 401.",
    });
  } else if (!original.enabled && current.enabled) {
    changes.push({
      id: "enabled",
      severity: "medium",
      label: "Key Will Be Enabled",
      description: "Incoming requests presenting this key will now be authorized and routed upstream.",
    });
  }

  // Request body logging enabled
  if (!original.policy.log_request_body && current.log_request_body) {
    changes.push({
      id: "log_req",
      severity: "high",
      label: "Request Body Logging Enabled",
      description: "Client request payloads may contain confidential prompts, user PII, or credentials which will be captured to SQLite.",
    });
  }

  // Response body logging enabled
  if (!original.policy.log_response_body && current.log_response_body) {
    changes.push({
      id: "log_res",
      severity: "high",
      label: "Response Body Logging Enabled",
      description: "Upstream model response payloads will be stored in SQLite and may contain sensitive generated output.",
    });
  }

  // Allowlist removed (previously restricted models, now empty = all allowed)
  if (original.policy.allowed_models.length > 0 && current.allowed_models.length === 0) {
    changes.push({
      id: "allowlist_removed",
      severity: "high",
      label: "Model Allowlist Removed",
      description: "All upstream models will now be accessible by this key (subject only to denylist rules).",
    });
  }

  // Denied models removed
  if (original.policy.denied_models.length > 0 && current.denied_models.length < original.policy.denied_models.length) {
    changes.push({
      id: "denylist_relaxed",
      severity: "medium",
      label: "Denied Models Relaxed",
      description: "One or more blocked model patterns were removed from the denylist.",
    });
  }

  // Concurrency removed
  if (original.policy.max_concurrent_requests > 0 && current.concurrency_mode === "unlimited") {
    changes.push({
      id: "concurrency_removed",
      severity: "medium",
      label: "Concurrency Cap Removed",
      description: "This key will now allow unlimited simultaneous in-flight requests.",
    });
  }

  return changes;
}
