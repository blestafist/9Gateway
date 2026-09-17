import { describe, it, expect } from "vitest";
import {
  formatTimestamp,
  formatExactTimestamp,
  formatDurationMicros,
  formatExactDurationMicros,
  formatDurationSeconds,
  formatCostMicros,
  formatExactCostMicros,
  formatTokenCount,
  formatExactTokenCount,
  formatByteCount,
  formatExactByteCount,
  formatHttpStatus,
  formatTerminalOutcome,
  formatNullable,
} from "./index";

describe("Formatters", () => {
  describe("Timestamps", () => {
    it("formats valid ISO timestamps", () => {
      const iso = "2026-09-17T14:30:00.000Z";
      const formatted = formatTimestamp(iso, "en-US");
      expect(formatted).not.toBe("—");
      expect(formatted).toContain("2026");
    });

    it("handles null, undefined, and invalid date strings", () => {
      expect(formatTimestamp(null)).toBe("—");
      expect(formatTimestamp(undefined)).toBe("—");
      expect(formatTimestamp("not-a-date")).toBe("—");
    });

    it("formats exact timestamps preserving ISO string", () => {
      expect(formatExactTimestamp("2026-09-17T14:30:00.000Z")).toBe("2026-09-17T14:30:00.000Z");
      expect(formatExactTimestamp(null)).toBe("—");
      expect(formatExactTimestamp(undefined)).toBe("—");
    });
  });

  describe("Durations", () => {
    it("distinguishes null from 0 micros", () => {
      expect(formatDurationMicros(null)).toBe("—");
      expect(formatDurationMicros(undefined)).toBe("—");
      expect(formatDurationMicros(0)).toBe("0 µs");
      expect(formatExactDurationMicros(null)).toBe("—");
      expect(formatExactDurationMicros(0)).toBe("0 µs");
    });

    it("scales microsecond durations appropriately", () => {
      expect(formatDurationMicros(500)).toBe("500 µs");
      expect(formatDurationMicros(1500)).toBe("1.5 ms");
      expect(formatDurationMicros(2000)).toBe("2 ms");
      expect(formatDurationMicros(1_500_000)).toBe("1.5 s");
      expect(formatDurationMicros(2_000_000)).toBe("2 s");
    });

    it("formats exact microseconds preserving precision", () => {
      expect(formatExactDurationMicros(1234567)).toBe("1,234,567 µs");
    });

    it("formats duration seconds into human units", () => {
      expect(formatDurationSeconds(null)).toBe("—");
      expect(formatDurationSeconds(0)).toBe("0s");
      expect(formatDurationSeconds(45)).toBe("45s");
      expect(formatDurationSeconds(120)).toBe("2m");
      expect(formatDurationSeconds(7200)).toBe("2h");
      expect(formatDurationSeconds(86400 * 3)).toBe("3d");
    });
  });

  describe("Costs", () => {
    it("distinguishes null from 0 cost", () => {
      expect(formatCostMicros(null)).toBe("—");
      expect(formatCostMicros(undefined)).toBe("—");
      expect(formatCostMicros(0)).toBe("$0.00");
      expect(formatExactCostMicros(null)).toBe("—");
      expect(formatExactCostMicros(0)).toBe("$0.000000 (0 µ$)");
    });

    it("formats fractional dollar amounts with appropriate decimals", () => {
      // 50 micros = $0.000050 (< $0.0001) -> 6 decimals
      expect(formatCostMicros(50)).toBe("$0.000050");
      // 5000 micros = $0.0050 (< $0.01) -> 4 decimals
      expect(formatCostMicros(5000)).toBe("$0.0050");
      // 1,500,000 micros = $1.50 (>= $0.01) -> 2 decimals
      expect(formatCostMicros(1_500_000)).toBe("$1.50");
    });

    it("formats exact costs preserving integer micros", () => {
      expect(formatExactCostMicros(8250)).toBe("$0.008250 (8,250 µ$)");
    });
  });

  describe("Tokens", () => {
    it("distinguishes null from 0 tokens", () => {
      expect(formatTokenCount(null)).toBe("—");
      expect(formatTokenCount(undefined)).toBe("—");
      expect(formatTokenCount(0)).toBe("0");
      expect(formatExactTokenCount(null)).toBe("—");
      expect(formatExactTokenCount(0)).toBe("0 tokens");
    });

    it("formats compact and exact token counts", () => {
      expect(formatTokenCount(1500, false)).toBe("1,500");
      expect(formatTokenCount(1500, true)).toBe("1.5k");
      expect(formatTokenCount(2_500_000, true)).toBe("2.5M");
      expect(formatExactTokenCount(1234567)).toBe("1,234,567 tokens");
    });
  });

  describe("Bytes", () => {
    it("distinguishes null from 0 bytes", () => {
      expect(formatByteCount(null)).toBe("—");
      expect(formatByteCount(undefined)).toBe("—");
      expect(formatByteCount(0)).toBe("0 B");
      expect(formatExactByteCount(null)).toBe("—");
      expect(formatExactByteCount(0)).toBe("0 bytes");
    });

    it("formats byte magnitudes cleanly", () => {
      expect(formatByteCount(512)).toBe("512 B");
      expect(formatByteCount(2048)).toBe("2 KB");
      expect(formatByteCount(2 * 1024 * 1024)).toBe("2 MB");
      expect(formatByteCount(2.5 * 1024 * 1024 * 1024)).toBe("2.5 GB");
      expect(formatExactByteCount(1048576)).toBe("1,048,576 bytes");
    });
  });

  describe("HTTP Status and Outcomes", () => {
    it("formats HTTP status and preserves null vs 0", () => {
      expect(formatHttpStatus(null)).toBe("—");
      expect(formatHttpStatus(undefined)).toBe("—");
      expect(formatHttpStatus(200)).toBe("200");
      expect(formatHttpStatus(0)).toBe("0");
    });

    it("formats known outcomes", () => {
      expect(formatTerminalOutcome("success")).toBe("Success");
      expect(formatTerminalOutcome("rate_limited")).toBe("Rate Limited");
      expect(formatTerminalOutcome("budget_exceeded")).toBe("Budget Exceeded");
      expect(formatTerminalOutcome("upstream_error")).toBe("Upstream Error");
      expect(formatTerminalOutcome("cursor_expired")).toBe("Cursor Expired");
    });

    it("safely formats unknown outcome enums", () => {
      expect(formatTerminalOutcome("new_unknown_gate_fail")).toBe("New Unknown Gate Fail");
      expect(formatTerminalOutcome("custom-provider-error")).toBe("Custom Provider Error");
      expect(formatTerminalOutcome(null)).toBe("—");
    });
  });

  describe("Nullable helper", () => {
    it("handles null, undefined, empty strings and fallback", () => {
      expect(formatNullable(null)).toBe("—");
      expect(formatNullable(undefined)).toBe("—");
      expect(formatNullable("   ")).toBe("—");
      expect(formatNullable(null, undefined, "N/A")).toBe("N/A");
      expect(formatNullable("hello")).toBe("hello");
      expect(formatNullable(42, (v) => `#${v}`)).toBe("#42");
    });
  });
});
