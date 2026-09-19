import { RequestBodyKind, RequestRangePreset } from "./types";

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

const STRICT_UTC_RFC3339 =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?Z$/;

/**
 * Request history bounds are deliberately narrower than Date.parse: bookmarks
 * must carry an explicit UTC timestamp and must not be silently normalized.
 */
export function parseStrictUtcRfc3339(
  val: string | null | undefined
): Date | null {
  if (!val || typeof val !== "string") return null;
  const match = STRICT_UTC_RFC3339.exec(val.trim());
  if (!match) return null;

  const [, year, month, day, hour, minute, second] = match;
  const date = new Date(val.trim());
  if (Number.isNaN(date.getTime())) return null;

  // Date.parse normalizes out-of-range calendar fields, so verify the fields
  // independently before accepting the value.
  if (
    date.getUTCFullYear() !== Number(year) ||
    date.getUTCMonth() + 1 !== Number(month) ||
    date.getUTCDate() !== Number(day) ||
    date.getUTCHours() !== Number(hour) ||
    date.getUTCMinutes() !== Number(minute) ||
    date.getUTCSeconds() !== Number(second)
  ) {
    return null;
  }

  return date;
}

export function isValidIsoDate(val: string | null | undefined): boolean {
  return parseStrictUtcRfc3339(val) !== null;
}

export function isValidCustomRequestRange(
  after: string | null | undefined,
  before: string | null | undefined
): boolean {
  const start = parseStrictUtcRfc3339(after);
  const end = parseStrictUtcRfc3339(before);
  return start !== null && end !== null && start.getTime() < end.getTime();
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

export const MAX_PREVIEW_BYTES = 256 * 1024; // 256 KiB = 262,144 bytes

export function formatBodyKindLabel(kind: RequestBodyKind): string {
  switch (kind) {
    case "client_request":
      return "Client Request";
    case "upstream_request":
      return "Upstream Request";
    case "response":
      return "Response";
    default:
      return kind;
  }
}

export function formatRawTextPreview(
  bytes: Uint8Array,
  maxBytes = MAX_PREVIEW_BYTES
): { text: string; isPreviewTruncated: boolean } {
  const isPreviewTruncated = bytes.length > maxBytes;
  const slice = isPreviewTruncated ? bytes.subarray(0, maxBytes) : bytes;

  // Use TextDecoder in non-fatal mode to replace invalid sequences with \uFFFD
  const decoder = new TextDecoder("utf-8", { fatal: false });
  const rawText = decoder.decode(slice);

  // Replace NUL bytes with visible representation symbol (U+2400)
  const safeText = rawText.replace(/\0/g, "\u2400");

  return { text: safeText, isPreviewTruncated };
}

export function formatHexPreview(
  bytes: Uint8Array,
  maxBytes = MAX_PREVIEW_BYTES
): { hex: string; isPreviewTruncated: boolean } {
  const isPreviewTruncated = bytes.length > maxBytes;
  const slice = isPreviewTruncated ? bytes.subarray(0, maxBytes) : bytes;

  const lines: string[] = [];
  const total = slice.length;

  for (let offset = 0; offset < total; offset += 16) {
    const chunk = slice.subarray(offset, Math.min(offset + 16, total));
    const offsetHex = offset.toString(16).padStart(8, "0");

    const byteHexes: string[] = [];
    let asciiChars = "";

    for (let i = 0; i < 16; i++) {
      if (i < chunk.length) {
        const b = chunk[i]!;
        byteHexes.push(b.toString(16).padStart(2, "0"));
        asciiChars += b >= 32 && b <= 126 ? String.fromCharCode(b) : ".";
      } else {
        byteHexes.push("  ");
      }
    }

    // Split 16 bytes into two groups of 8 with double space
    const firstGroup = byteHexes.slice(0, 8).join(" ");
    const secondGroup = byteHexes.slice(8, 16).join(" ");
    const hexGroup = `${firstGroup}  ${secondGroup}`;

    lines.push(`${offsetHex}  ${hexGroup}  |${asciiChars}|`);
  }

  return { hex: lines.join("\n"), isPreviewTruncated };
}

export function tryFormatPrettyJson(text: string): {
  isValid: boolean;
  pretty: string | null;
} {
  if (!text || !text.trim()) {
    return { isValid: false, pretty: null };
  }
  try {
    const parsed = JSON.parse(text);
    return { isValid: true, pretty: JSON.stringify(parsed, null, 2) };
  } catch {
    return { isValid: false, pretty: null };
  }
}

export function downloadBodyBytes(
  bytes: Uint8Array,
  filename: string,
  contentType = "application/octet-stream"
): void {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return;
  }
  const blob = new Blob([bytes as unknown as BlobPart], { type: contentType });
  if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.style.display = "none";
    document.body.appendChild(a);
    try {
      a.click();
    } catch {
      // jsdom environment throws on synthetic anchor navigation
    }
    document.body.removeChild(a);
    setTimeout(() => {
      URL.revokeObjectURL(url);
    }, 1000);
  }
}
