import { formatCostMicros } from "../../shared/formatters";

export type DeltaDirection = "increase" | "decrease" | "unchanged" | "unavailable";
export type DeltaSentiment = "neutral" | "warning" | "positive" | "unavailable";

export interface DeltaCalculationOptions {
  isErrorMetric?: boolean;
  isCost?: boolean;
  unit?: string;
}

export interface DeltaResult {
  direction: DeltaDirection;
  sentiment: DeltaSentiment;
  percentage: number | null;
  formattedPercentage: string | null;
  comparisonText: string;
  isUnavailable: boolean;
  diff: number | null;
}

/**
 * Calculates previous-period deltas according to gateway observability requirements:
 * - Neutral direction for non-judgmental metrics (requests, tokens, cost).
 * - Warning semantics for error/rejection increases.
 * - Zero or unknown baseline is marked unavailable, NEVER infinity or misleading +100%.
 * - Costs explicitly estimated in comparison text.
 */
export function calculateDelta(
  current: number | null | undefined,
  previous: number | null | undefined,
  options: DeltaCalculationOptions = {}
): DeltaResult {
  const { isErrorMetric = false, isCost = false } = options;

  if (current === null || current === undefined || previous === null || previous === undefined) {
    return {
      direction: "unavailable",
      sentiment: "unavailable",
      percentage: null,
      formattedPercentage: null,
      comparisonText: "Previous period unavailable",
      isUnavailable: true,
      diff: null,
    };
  }

  // Zero baseline rule: baseline is unavailable, never infinity or misleading 100%
  if (previous === 0) {
    if (current === 0) {
      return {
        direction: "unchanged",
        sentiment: "neutral",
        percentage: null,
        formattedPercentage: null,
        comparisonText: isCost ? "0 in previous period (est.)" : "0 in previous period",
        isUnavailable: true,
        diff: 0,
      };
    }

    return {
      direction: "increase",
      sentiment: isErrorMetric ? "warning" : "neutral",
      percentage: null,
      formattedPercentage: null,
      comparisonText: isCost
        ? "No baseline (0 in prev period, est.)"
        : "No baseline (0 in prev period)",
      isUnavailable: true,
      diff: current,
    };
  }

  const diff = current - previous;
  const percentage = (diff / previous) * 100;
  const sign = percentage > 0 ? "+" : "";
  const formattedPercentage = `${sign}${percentage.toFixed(1)}%`;

  let direction: DeltaDirection = "unchanged";
  let sentiment: DeltaSentiment = "neutral";

  if (diff > 0) {
    direction = "increase";
    sentiment = isErrorMetric ? "warning" : "neutral";
  } else if (diff < 0) {
    direction = "decrease";
    sentiment = isErrorMetric ? "positive" : "neutral";
  } else {
    direction = "unchanged";
    sentiment = "neutral";
  }

  let formattedPrev = previous.toLocaleString();
  if (isCost) {
    formattedPrev = formatCostMicros(previous);
  }

  const comparisonText = isCost
    ? `${formattedPercentage} vs. prev est. (${formattedPrev})`
    : `${formattedPercentage} vs. prev (${formattedPrev})`;

  return {
    direction,
    sentiment,
    percentage,
    formattedPercentage,
    comparisonText,
    isUnavailable: false,
    diff,
  };
}
