const KNOWN_OUTCOMES: Record<string, string> = {
  success: "Success",
  model_rejected: "Model Rejected",
  rate_limited: "Rate Limited",
  token_limited: "Token Limited",
  budget_exceeded: "Budget Exceeded",
  concurrency_limited: "Concurrency Limited",
  upstream_error: "Upstream Error",
  upstream_timeout: "Upstream Timeout",
  client_cancelled: "Client Cancelled",
  response_transport_error: "Transport Error",
  internal_error: "Internal Error",
  cursor_expired: "Cursor Expired",
  key_disabled: "Key Disabled",
  key_expired: "Key Expired",
  invalid_request: "Invalid Request",
};

export function formatHttpStatus(status: number | null | undefined): string {
  if (status === null || status === undefined) {
    return "—";
  }
  return String(status);
}

export function formatTerminalOutcome(outcome: string | null | undefined): string {
  if (!outcome) {
    return "—";
  }

  const normalized = outcome.toLowerCase().trim();
  if (KNOWN_OUTCOMES[normalized]) {
    return KNOWN_OUTCOMES[normalized];
  }

  // Gracefully handle unknown enums by formatting into title case
  return normalized
    .split(/[-_]/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
