import { formatCostMicros, formatTokenCount } from "../../shared/formatters";

export type DeltaDirection = "increase" | "decrease" | "unchanged" | "unavailable";
export type DeltaSentiment = "neutral" | "warning" | "positive" | "unavailable";

export interface UsageDeltaResult {
  direction: DeltaDirection;
  sentiment: DeltaSentiment;
  percentage: number | null;
  formattedPercentage: string | null;
  comparisonText: string;
  isUnavailable: boolean;
  diff: number | null;
}

export interface UsageDeltaOptions {
  isErrorMetric?: boolean;
  isCost?: boolean;
  isTokens?: boolean;
}

export function calculateUsageDelta(
  current: number | null | undefined,
  previous: number | null | undefined,
  options: UsageDeltaOptions = {}
): UsageDeltaResult {
  const { isErrorMetric = false, isCost = false, isTokens = false } = options;

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
  } else if (isTokens) {
    formattedPrev = formatTokenCount(previous, true);
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
