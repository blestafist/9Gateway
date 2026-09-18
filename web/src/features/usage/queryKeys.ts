import { UsageTimeseriesParams, UsageBreakdownParams } from "./types";

export const usageQueryKeys = {
  all: ["usage"] as const,
  timeseries: (params: UsageTimeseriesParams) =>
    [
      "usage",
      "timeseries",
      params.after ?? null,
      params.before ?? null,
      params.bucket ?? "auto",
    ] as const,
  breakdown: (params: UsageBreakdownParams) =>
    [
      "usage",
      "breakdown",
      params.group_by,
      params.after ?? null,
      params.before ?? null,
    ] as const,
  previousComparison: (params: { after: string; before: string }) =>
    ["usage", "previous-comparison", params.after, params.before] as const,
};
