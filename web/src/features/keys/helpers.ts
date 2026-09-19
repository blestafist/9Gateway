import { formatTimestamp } from "../../shared/formatters";
import { AdminKeyListItem, KeyPageFilters } from "./types";

export interface KeyStatusInfo {
  status: "active" | "disabled" | "expired" | "expiring";
  label: string;
  variant: "success" | "warning" | "danger" | "neutral";
  isExpired: boolean;
  isExpiringSoon: boolean;
  expiresText: string;
}

export function isKeyExpired(expiresAt: string | null, nowMs: number = Date.now()): boolean {
  if (!expiresAt) return false;
  const time = new Date(expiresAt).getTime();
  return !Number.isNaN(time) && time <= nowMs;
}

export function isKeyExpiringSoon(
  expiresAt: string | null,
  nowMs: number = Date.now(),
  thresholdDays = 30
): boolean {
  if (!expiresAt) return false;
  const time = new Date(expiresAt).getTime();
  if (Number.isNaN(time) || time <= nowMs) return false;
  return time - nowMs <= thresholdDays * 24 * 60 * 60 * 1000;
}

export function getKeyStatus(
  enabled: boolean,
  expiresAt: string | null,
  nowMs: number = Date.now()
): KeyStatusInfo {
  if (isKeyExpired(expiresAt, nowMs)) {
    return {
      status: "expired",
      label: "Expired",
      variant: "danger",
      isExpired: true,
      isExpiringSoon: false,
      expiresText: "Expired",
    };
  }

  if (isKeyExpiringSoon(expiresAt, nowMs)) {
    if (!enabled) {
      return {
        status: "disabled",
        label: "Disabled (Expiring)",
        variant: "warning",
        isExpired: false,
        isExpiringSoon: true,
        expiresText: "Expiring soon",
      };
    }
    return {
      status: "expiring",
      label: "Expiring Soon",
      variant: "warning",
      isExpired: false,
      isExpiringSoon: true,
      expiresText: "Expiring soon",
    };
  }

  if (!enabled) {
    return {
      status: "disabled",
      label: "Disabled",
      variant: "neutral",
      isExpired: false,
      isExpiringSoon: false,
      expiresText: expiresAt ? "Valid until expiry" : "Never expires",
    };
  }

  return {
    status: "active",
    label: "Active",
    variant: "success",
    isExpired: false,
    isExpiringSoon: false,
    expiresText: expiresAt ? "Active" : "Never expires",
  };
}

export function formatKeyExpiry(expiresAt: string | null): string {
  if (!expiresAt) {
    return "Never";
  }
  return formatTimestamp(expiresAt);
}

export function filterKeysOnPage(
  keys: AdminKeyListItem[],
  filters: KeyPageFilters,
  nowMs: number = Date.now()
): AdminKeyListItem[] {
  const query = filters.searchQuery.trim().toLowerCase();

  return keys.filter((key) => {
    // 1. Search Query filter (matches name, id, or display_prefix)
    if (query) {
      const matchName = key.name.toLowerCase().includes(query);
      const matchId = key.id.toLowerCase().includes(query);
      const matchPrefix = key.display_prefix.toLowerCase().includes(query);
      if (!matchName && !matchId && !matchPrefix) {
        return false;
      }
    }

    // 2. Status Filter
    if (filters.status !== "all") {
      const statusInfo = getKeyStatus(key.enabled, key.expires_at, nowMs);
      if (filters.status === "active" && statusInfo.status !== "active") {
        return false;
      }
      if (filters.status === "disabled" && statusInfo.status !== "disabled") {
        return false;
      }
      if (filters.status === "expired" && statusInfo.status !== "expired") {
        return false;
      }
      if (filters.status === "expiring" && statusInfo.status !== "expiring") {
        return false;
      }
    }

    // 3. Policy Filter
    if (filters.policy !== "all") {
      if (filters.policy === "allowlist" && !key.policy_summary.allow_models) {
        return false;
      }
      if (filters.policy === "denylist" && !key.policy_summary.deny_models) {
        return false;
      }
      if (filters.policy === "log_req" && !key.policy_summary.log_request_body) {
        return false;
      }
      if (filters.policy === "log_res" && !key.policy_summary.log_response_body) {
        return false;
      }
    }

    return true;
  });
}

export function generateKeyDownloadFilename(name: string, prefixOrId: string): string {
  const sanitizedName = name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  const base = sanitizedName || "api-key";
  const sanitizedSuffix = prefixOrId
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9_-]+/g, "");
  return `9gateway-key-${base}${sanitizedSuffix ? `-${sanitizedSuffix}` : ""}.txt`;
}

export function downloadKeySecret(filename: string, secret: string): void {
  const blob = new Blob([secret + "\n"], { type: "text/plain;charset=utf-8" });
  if (typeof window !== "undefined" && typeof URL.createObjectURL === "function") {
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    document.body.removeChild(anchor);
    URL.revokeObjectURL(url);
  }
}

/**
 * Parses a custom expiration date string into epoch milliseconds.
 * If the input has a time component but lacks an explicit timezone (e.g. standard datetime-local "YYYY-MM-DDTHH:mm"),
 * it is explicitly interpreted as UTC rather than browser local time.
 * If the input already contains a timezone offset (e.g. "Z", "+02:00", "-05:00"), it is parsed preserving that offset.
 */
export function parseCustomExpiryToMs(customVal: string): number {
  const trimmed = customVal.trim();
  if (!trimmed) {
    return NaN;
  }
  const hasTime = /[T\s]\d{1,2}:\d{2}/.test(trimmed);
  const hasTimezone = hasTime && /(?:Z|[+-]\d{2}(?::?\d{2})?)$/i.test(trimmed);

  let parseable = trimmed;
  if (hasTime && !hasTimezone) {
    parseable = trimmed.replace(/^(\d{4}-\d{2}-\d{2})\s+/, "$1T") + "Z";
  }
  return new Date(parseable).getTime();
}

/**
 * Validates a custom expiration date string.
 * Returns an error message string if invalid, or null if valid.
 */
export function validateCustomExpiry(customVal: string, nowMs: number = Date.now()): string | null {
  const trimmed = customVal.trim();
  if (!trimmed) {
    return "Please enter an expiration date and time.";
  }
  const time = parseCustomExpiryToMs(trimmed);
  if (Number.isNaN(time)) {
    return "Invalid expiration date format.";
  }
  if (time <= nowMs) {
    return "Expiration date must be in the future.";
  }
  return null;
}


