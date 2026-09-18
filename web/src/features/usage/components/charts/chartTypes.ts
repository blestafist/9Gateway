import { UsageTimeseriesBucket } from "../../types";

export interface ChartTimePoint {
  timeLabel: string;
  startIso: string;
  endIso: string;
}

export interface RequestsChartPoint extends ChartTimePoint {
  successful: number;
  error: number;
  rejected: number;
  total: number;
}

export interface TokensChartPoint extends ChartTimePoint {
  input: number | null;
  cached: number | null;
  output: number | null;
  total: number | null;
}

export interface CostChartPoint extends ChartTimePoint {
  costDollars: number | null;
  costMicros: number | null;
}

export interface LatencyChartPoint extends ChartTimePoint {
  totalMs: number | null;
  ttfbMs: number | null;
  upstreamMs: number | null;
  totalSamples: number;
  ttfbSamples: number;
  upstreamSamples: number;
}

export function formatBucketTimeLabel(isoString: string): string {
  try {
    const d = new Date(isoString);
    if (Number.isNaN(d.getTime())) return isoString;
    const hours = String(d.getUTCHours()).padStart(2, "0");
    const minutes = String(d.getUTCMinutes()).padStart(2, "0");
    const month = d.toLocaleString("en-US", { month: "short", timeZone: "UTC" });
    const day = d.getUTCDate();
    return `${month} ${day} ${hours}:${minutes}`;
  } catch {
    return isoString;
  }
}

export function transformTimeseriesBuckets(buckets: UsageTimeseriesBucket[]) {
  const requests: RequestsChartPoint[] = [];
  const tokens: TokensChartPoint[] = [];
  const cost: CostChartPoint[] = [];
  const latency: LatencyChartPoint[] = [];

  for (const b of buckets) {
    const timeLabel = formatBucketTimeLabel(b.bucket_start);
    const common: ChartTimePoint = {
      timeLabel,
      startIso: b.bucket_start,
      endIso: b.bucket_end,
    };

    requests.push({
      ...common,
      successful: b.successful_requests,
      error: b.error_requests,
      rejected: b.rejected_requests,
      total: b.total_requests,
    });

    const totalTokens =
      b.input_tokens !== null && b.output_tokens !== null
        ? b.input_tokens + b.output_tokens
        : null;

    tokens.push({
      ...common,
      input: b.input_tokens,
      cached: b.cached_input_tokens,
      output: b.output_tokens,
      total: totalTokens,
    });

    cost.push({
      ...common,
      costDollars: b.cost_micros !== null ? b.cost_micros / 1_000_000 : null,
      costMicros: b.cost_micros,
    });

    latency.push({
      ...common,
      totalMs:
        b.total_latency_samples > 0 && b.avg_total_latency_micros !== null
          ? Number((b.avg_total_latency_micros / 1000).toFixed(2))
          : null,
      ttfbMs:
        b.ttfb_latency_samples > 0 && b.avg_ttfb_latency_micros !== null
          ? Number((b.avg_ttfb_latency_micros / 1000).toFixed(2))
          : null,
      upstreamMs:
        b.upstream_latency_samples > 0 && b.avg_upstream_latency_micros !== null
          ? Number((b.avg_upstream_latency_micros / 1000).toFixed(2))
          : null,
      totalSamples: b.total_latency_samples,
      ttfbSamples: b.ttfb_latency_samples,
      upstreamSamples: b.upstream_latency_samples,
    });
  }

  return { requests, tokens, cost, latency };
}
