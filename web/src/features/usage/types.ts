export type UsageRangePreset =
  | "1h"
  | "today"
  | "24h"
  | "7d"
  | "30d"
  | "90d"
  | "1y"
  | "all"
  | "custom";

export type UsageBucketResolution =
  | "auto"
  | "five_minutes"
  | "hour"
  | "day"
  | "week"
  | "month";

export type UsageChartMetric = "requests" | "tokens" | "cost" | "latency";

export type UsageRankingDimension = "model" | "key" | "outcome";

export type UsageRankingMetric = "requests" | "tokens" | "cost";

export interface UsageTimeseriesBucket {
  bucket_start: string;
  bucket_end: string;
  total_requests: number;
  successful_requests: number;
  error_requests: number;
  rejected_requests: number;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  output_tokens: number | null;
  cost_micros: number | null;
  avg_total_latency_micros: number | null;
  total_latency_samples: number;
  avg_ttfb_latency_micros: number | null;
  ttfb_latency_samples: number;
  avg_upstream_latency_micros: number | null;
  upstream_latency_samples: number;
}

export interface UsageTimeseriesResponse {
  requested_after: string | null;
  effective_after: string;
  before: string;
  bucket: string;
  retention_limited: boolean;
  earliest_retained_at: string | null;
  latest_retained_at: string | null;
  buckets: UsageTimeseriesBucket[];
}

export interface UsageBreakdownRow {
  id: string;
  name: string;
  key_id?: string;
  is_unknown: boolean;
  is_deleted: boolean;
  total_requests: number;
  successful_requests: number;
  error_requests: number;
  rejected_requests: number;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  output_tokens: number | null;
  cost_micros: number | null;
}

export interface UsageBreakdownTotal {
  total_requests: number;
  successful_requests: number;
  error_requests: number;
  rejected_requests: number;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  output_tokens: number | null;
  cost_micros: number | null;
}

export interface UsageBreakdownResponse {
  requested_after: string | null;
  effective_after: string;
  before: string;
  group_by: string;
  retention_limited: boolean;
  earliest_retained_at: string | null;
  latest_retained_at: string | null;
  rows: UsageBreakdownRow[];
  other: UsageBreakdownRow;
  total: UsageBreakdownTotal;
}

export interface UsageTimeseriesParams {
  after?: string;
  before?: string;
  bucket?: string;
}

export interface UsageBreakdownParams {
  after?: string;
  before?: string;
  group_by: UsageRankingDimension;
}
