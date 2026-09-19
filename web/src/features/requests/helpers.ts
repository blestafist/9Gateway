import { RequestRangePreset } from "./types";

export interface RangeBounds {
  after?: string;
  before?: string;
}

export function computeRequestPresetBounds(
  preset: RequestRangePreset,
  now: Date = new Date()
): RangeBounds {
  switch (preset) {
    case "1h": {
      const after = new Date(now.getTime() - 60 * 60 * 1000).toISOString();
      const before = now.toISOString();
      return { after, before };
    }
    case "24h": {
      const after = new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString();
      const before = now.toISOString();
      return { after, before };
    }
    case "7d": {
      const after = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString();
      const before = now.toISOString();
      return { after, before };
    }
    case "30d": {
      const after = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000).toISOString();
      const before = now.toISOString();
      return { after, before };
    }
    case "all":
    case "custom":
    default:
      return {};
  }
}

export function isValidIsoDate(val: string | null | undefined): boolean {
  if (!val || typeof val !== "string" || !val.trim()) {
    return false;
  }
  const date = new Date(val.trim());
  return !Number.isNaN(date.getTime());
}

export function truncateId(id: string, head = 8, tail = 6): string {
  if (!id) return "";
  if (id.length <= head + tail) return id;
  return `${id.slice(0, head)}…${id.slice(-tail)}`;
}

export function getOutcomeVariant(
  status: number | null | undefined,
  outcome: string | null | undefined
): "success" | "warning" | "danger" | "neutral" {
  const normOutcome = outcome ? outcome.toLowerCase().trim() : "";

  if (
    normOutcome === "upstream_error" ||
    normOutcome === "response_error" ||
    normOutcome === "internal_error" ||
    normOutcome === "upstream_timeout" ||
    normOutcome === "response_transport_error" ||
    (status !== null && status !== undefined && status >= 500)
  ) {
    return "danger";
  }

  if (
    normOutcome === "rate_limited" ||
    normOutcome === "token_limited" ||
    normOutcome === "budget_exceeded" ||
    normOutcome === "concurrency_limited" ||
    normOutcome === "pre_upstream" ||
    normOutcome === "model_rejected" ||
    normOutcome === "invalid_request" ||
    normOutcome === "key_disabled" ||
    normOutcome === "key_expired" ||
    (status !== null && status !== undefined && status >= 400 && status < 500)
  ) {
    return "warning";
  }

  if (
    normOutcome === "complete" ||
    normOutcome === "success" ||
    normOutcome === "custom_dispatch" ||
    (status !== null && status !== undefined && status >= 200 && status < 300)
  ) {
    return "success";
  }

  return "neutral";
}

export function formatModes(
  requested: string | null | undefined,
  upstream: string | null | undefined,
  delivered: string | null | undefined
): string {
  if (!requested && !upstream && !delivered) {
    return "—";
  }

  if (requested && delivered && requested !== delivered) {
    return `${requested} → ${delivered}`;
  }

  return delivered || requested || upstream || "—";
}
