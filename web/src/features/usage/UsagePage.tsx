import React, { useState, useMemo, useCallback, useEffect, useRef } from "react";
import { useSearchParams, Link } from "react-router-dom";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { Alert, Button, Skeleton } from "../../shared/ui";
import { AdminApiError } from "../../shared/transport";
import {
  UsageRangePreset,
  UsageBucketResolution,
  UsageChartMetric,
  UsageRankingDimension,
  UsageRankingMetric,
} from "./types";
import { usageQueryKeys } from "./queryKeys";
import { getUsageTimeseries, getUsageBreakdown } from "./api";
import {
  computePresetBounds,
  computePreviousBounds,
  getValidBuckets,
  normalizeBucketResolution,
} from "./ranges";
import { calculateUsageDelta } from "./deltas";
import { UsageControls } from "./components/UsageControls";
import { RetentionNotice } from "./components/RetentionNotice";
import { UsageKpiCards, UsageKpisData } from "./components/UsageKpiCards";
import { LazyChartCard } from "./components/charts/LazyChartCard";
import { RankingsCard } from "./components/RankingsCard";
import { AlertTriangle, LogIn, RefreshCw, WifiOff } from "lucide-react";
import "./usage.css";

export const UsagePage: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams();

  const [isOnline, setIsOnline] = useState<boolean>(() =>
    typeof navigator !== "undefined" ? navigator.onLine : true
  );

  useEffect(() => {
    const handleOnline = () => setIsOnline(true);
    const handleOffline = () => setIsOnline(false);

    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);

    return () => {
      window.removeEventListener("online", handleOnline);
      window.removeEventListener("offline", handleOffline);
    };
  }, []);

  // URL search parameter parsing with defaults
  const rangeParam = searchParams.get("range") as UsageRangePreset | null;
  const activePreset: UsageRangePreset =
    rangeParam &&
    [
      "1h",
      "today",
      "24h",
      "7d",
      "30d",
      "90d",
      "1y",
      "all",
      "custom",
    ].includes(rangeParam)
      ? rangeParam
      : "24h";

  const fromParam = searchParams.get("from") || "";
  const toParam = searchParams.get("to") || "";

  const requestedBucket = searchParams.get("bucket") as UsageBucketResolution | null;
  const bucketParam: UsageBucketResolution = [
    "auto",
    "five_minutes",
    "hour",
    "day",
    "week",
    "month",
  ].includes(requestedBucket ?? "")
    ? (requestedBucket as UsageBucketResolution)
    : "auto";

  const metricParam = searchParams.get("metric") as UsageChartMetric | null;
  const activeMetric: UsageChartMetric =
    metricParam && ["requests", "tokens", "cost", "latency"].includes(metricParam)
      ? metricParam
      : "tokens";

  const rankDimParam = searchParams.get("rank_dim") as UsageRankingDimension | null;
  const activeRankDim: UsageRankingDimension =
    rankDimParam && ["model", "key", "outcome"].includes(rankDimParam)
      ? rankDimParam
      : "model";

  const rankMetricParam = searchParams.get("rank_metric") as UsageRankingMetric | null;
  const activeRankMetric: UsageRankingMetric =
    rankMetricParam && ["requests", "tokens", "cost"].includes(rankMetricParam)
      ? rankMetricParam
      : "requests";

  // Compute fixed time bounds snapshot for the active preset
  const computeBounds = useCallback(
    (preset: UsageRangePreset, from: string, to: string) => {
      if (preset === "custom" && from && to) {
        return { after: from, before: to };
      }
      return computePresetBounds(preset);
    },
    []
  );

  const [bounds, setBounds] = useState(() =>
    computeBounds(activePreset, fromParam, toParam)
  );
  const boundsUrlKey = `${activePreset}|${fromParam}|${toParam}`;
  const previousBoundsUrlKey = useRef(boundsUrlKey);

  // URL changes (including browser back/forward) are the source of truth. Keep
  // preset windows stable until the user changes the URL or explicitly refreshes.
  useEffect(() => {
    if (previousBoundsUrlKey.current === boundsUrlKey) return;
    previousBoundsUrlKey.current = boundsUrlKey;
    setBounds(computeBounds(activePreset, fromParam, toParam));
  }, [activePreset, boundsUrlKey, computeBounds, fromParam, toParam]);

  const customDurationMs = useMemo(() => {
    if (activePreset !== "custom" || !fromParam || !toParam) return undefined;
    const fromMs = Date.parse(fromParam);
    const toMs = Date.parse(toParam);
    return Number.isFinite(fromMs) && Number.isFinite(toMs) && toMs > fromMs
      ? toMs - fromMs
      : undefined;
  }, [activePreset, fromParam, toParam]);
  const normalizedBucket = normalizeBucketResolution(
    activePreset,
    requestedBucket,
    customDurationMs
  );

  useEffect(() => {
    if (requestedBucket === normalizedBucket || (requestedBucket === null && normalizedBucket === "auto")) return;
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (normalizedBucket === "auto") next.delete("bucket");
      else next.set("bucket", normalizedBucket);
      return next;
    }, { replace: true });
  }, [normalizedBucket, requestedBucket, setSearchParams]);

  // Sync snapshot when preset or custom params change
  const handlePresetChange = (nextPreset: UsageRangePreset) => {
    const nextBounds = computeBounds(nextPreset, fromParam, toParam);
    setBounds(nextBounds);

    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (nextPreset === "24h") {
        next.delete("range");
      } else {
        next.set("range", nextPreset);
      }
      if (nextPreset !== "custom") {
        next.delete("from");
        next.delete("to");
      }
      // Reset bucket if invalid for the new preset
      const valid = getValidBuckets(nextPreset);
      if (!valid.includes(bucketParam)) {
        next.delete("bucket");
      }
      return next;
    });
  };

  const handleCustomRangeApply = (from: string, to: string) => {
    setBounds({ after: from, before: to });
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set("range", "custom");
      next.set("from", from);
      next.set("to", to);
      return next;
    });
  };

  const handleBucketChange = (nextBucket: UsageBucketResolution) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (nextBucket === "auto") {
        next.delete("bucket");
      } else {
        next.set("bucket", nextBucket);
      }
      return next;
    });
  };

  const handleMetricChange = (nextMetric: UsageChartMetric) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (nextMetric === "tokens") {
        next.delete("metric");
      } else {
        next.set("metric", nextMetric);
      }
      return next;
    });
  };

  const handleRankDimChange = (nextDim: UsageRankingDimension) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (nextDim === "model") {
        next.delete("rank_dim");
      } else {
        next.set("rank_dim", nextDim);
      }
      return next;
    });
  };

  const handleRankMetricChange = (nextMetric: UsageRankingMetric) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (nextMetric === "requests") {
        next.delete("rank_metric");
      } else {
        next.set("rank_metric", nextMetric);
      }
      return next;
    });
  };

  // Primary timeseries query (cancellation via signal, NO automatic polling)
  const timeseriesQuery = useQuery({
    queryKey: usageQueryKeys.timeseries({
      after: bounds.after,
      before: bounds.before,
      bucket: normalizedBucket,
    }),
    queryFn: ({ signal }) =>
      getUsageTimeseries(
        {
          after: bounds.after,
          before: bounds.before,
          bucket: normalizedBucket === "auto" ? undefined : normalizedBucket,
        },
        signal
      ),
    placeholderData: keepPreviousData,
    refetchInterval: false,
    refetchOnWindowFocus: false,
  });

  // Secondary breakdown query (cancellation via signal, NO automatic polling)
  const breakdownQuery = useQuery({
    queryKey: usageQueryKeys.breakdown({
      group_by: activeRankDim,
      after: bounds.after,
      before: bounds.before,
    }),
    queryFn: ({ signal }) =>
      getUsageBreakdown(
        {
          group_by: activeRankDim,
          after: bounds.after,
          before: bounds.before,
        },
        signal
      ),
    placeholderData: keepPreviousData,
    refetchInterval: false,
    refetchOnWindowFocus: false,
  });

  // Previous range comparison query for honest deltas (only if bounds.after is present)
  const prevBounds = useMemo(() => computePreviousBounds(bounds), [bounds]);

  const previousTimeseriesQuery = useQuery({
    queryKey: prevBounds
      ? usageQueryKeys.previousComparison(prevBounds)
      : ["usage", "previous-comparison", "disabled"],
    queryFn: ({ signal }) =>
      getUsageTimeseries(
        {
          after: prevBounds!.after,
          before: prevBounds!.before,
          bucket: "auto",
        },
        signal
      ),
    enabled: Boolean(prevBounds),
    // Never retain a previous comparison for a different window: that would
    // present a stale delta as if it matched the current range.
    placeholderData: undefined,
    refetchInterval: false,
    refetchOnWindowFocus: false,
  });

  // Manual refresh trigger
  const handleRefresh = () => {
    if (activePreset !== "custom") {
      const freshBounds = computePresetBounds(activePreset);
      const boundsChanged =
        freshBounds.after !== bounds.after || freshBounds.before !== bounds.before;
      if (boundsChanged) {
        setBounds(freshBounds);
        return;
      }
    }

    timeseriesQuery.refetch();
    breakdownQuery.refetch();
    if (prevBounds) {
      previousTimeseriesQuery.refetch();
    }
  };

  const isFetching =
    timeseriesQuery.isFetching ||
    breakdownQuery.isFetching ||
    previousTimeseriesQuery.isFetching;

  // Aggregate current KPI statistics
  const kpiData: UsageKpisData = useMemo(() => {
    const buckets = timeseriesQuery.data?.buckets || [];
    let totalRequests = 0;
    let successfulRequests = 0;
    let errorRequests = 0;
    let rejectedRequests = 0;
    let peakBucketRequests = 0;
    let inputTokenSum = 0;
    let cachedInputTokenSum = 0;
    let outputTokenSum = 0;
    let inputTokensKnown = true;
    let cachedInputTokensKnown = true;
    let outputTokensKnown = true;
    let costSum: number | null = null;
    let hasCost = false;

    let totalLatencySamples = 0;
    let totalWeightedLatency = 0;
    let ttfbSamples = 0;
    let ttfbWeightedLatency = 0;
    let upstreamSamples = 0;
    let upstreamWeightedLatency = 0;

    for (const b of buckets) {
      totalRequests += b.total_requests;
      successfulRequests += b.successful_requests;
      errorRequests += b.error_requests;
      rejectedRequests += b.rejected_requests;
      if (b.total_requests > peakBucketRequests) {
        peakBucketRequests = b.total_requests;
      }
      if (b.input_tokens === null) inputTokensKnown = false;
      else inputTokenSum += b.input_tokens;
      if (b.cached_input_tokens === null) cachedInputTokensKnown = false;
      else cachedInputTokenSum += b.cached_input_tokens;
      if (b.output_tokens === null) outputTokensKnown = false;
      else outputTokenSum += b.output_tokens;

      if (b.cost_micros !== null) {
        costSum = (costSum ?? 0) + b.cost_micros;
        hasCost = true;
      }

      if (b.total_latency_samples > 0 && b.avg_total_latency_micros !== null) {
        totalLatencySamples += b.total_latency_samples;
        totalWeightedLatency += b.avg_total_latency_micros * b.total_latency_samples;
      }
      if (b.ttfb_latency_samples > 0 && b.avg_ttfb_latency_micros !== null) {
        ttfbSamples += b.ttfb_latency_samples;
        ttfbWeightedLatency += b.avg_ttfb_latency_micros * b.ttfb_latency_samples;
      }
      if (b.upstream_latency_samples > 0 && b.avg_upstream_latency_micros !== null) {
        upstreamSamples += b.upstream_latency_samples;
        upstreamWeightedLatency += b.avg_upstream_latency_micros * b.upstream_latency_samples;
      }
    }

    return {
      totalRequests,
      successfulRequests,
      errorRequests,
      rejectedRequests,
      peakBucketRequests,
      inputTokens: inputTokensKnown ? inputTokenSum : null,
      cachedInputTokens: cachedInputTokensKnown ? cachedInputTokenSum : null,
      outputTokens: outputTokensKnown ? outputTokenSum : null,
      totalCostMicros: hasCost ? costSum : null,
      avgTotalLatencyMicros:
        totalLatencySamples > 0 ? Math.round(totalWeightedLatency / totalLatencySamples) : null,
      totalLatencySamples,
      avgTtfbLatencyMicros:
        ttfbSamples > 0 ? Math.round(ttfbWeightedLatency / ttfbSamples) : null,
      avgUpstreamLatencyMicros:
        upstreamSamples > 0 ? Math.round(upstreamWeightedLatency / upstreamSamples) : null,
    };
  }, [timeseriesQuery.data]);

  // Aggregate previous period totals for deltas
  const prevTotals = useMemo(() => {
    const buckets = previousTimeseriesQuery.data?.buckets;
    if (!buckets) return null;

    let totalReq = 0;
    let totalTokens = 0;
    let tokensKnown = true;
    let costSum: number | null = null;
    let hasCost = false;
    let errorReq = 0;

    for (const b of buckets) {
      totalReq += b.total_requests;
      if (b.input_tokens === null || b.output_tokens === null) tokensKnown = false;
      else totalTokens += b.input_tokens + b.output_tokens;
      errorReq += b.error_requests + b.rejected_requests;
      if (b.cost_micros !== null) {
        costSum = (costSum ?? 0) + b.cost_micros;
        hasCost = true;
      }
    }

    return {
      requests: totalReq,
      tokens: tokensKnown ? totalTokens : null,
      costMicros: hasCost ? costSum : null,
      errors: errorReq,
    };
  }, [previousTimeseriesQuery.data]);

  // Calculate honest deltas
  const requestDelta = useMemo(
    () => calculateUsageDelta(kpiData.totalRequests, prevTotals?.requests),
    [kpiData.totalRequests, prevTotals]
  );
  const tokenDelta = useMemo(
    () =>
      calculateUsageDelta(
        kpiData.inputTokens !== null || kpiData.outputTokens !== null
          ? (kpiData.inputTokens ?? 0) + (kpiData.outputTokens ?? 0)
          : null,
        prevTotals?.tokens,
        { isTokens: true }
      ),
    [kpiData.inputTokens, kpiData.outputTokens, prevTotals]
  );
  const costDelta = useMemo(
    () =>
      calculateUsageDelta(kpiData.totalCostMicros, prevTotals?.costMicros, {
        isCost: true,
      }),
    [kpiData.totalCostMicros, prevTotals]
  );
  const errorDelta = useMemo(
    () =>
      calculateUsageDelta(
        kpiData.errorRequests + kpiData.rejectedRequests,
        prevTotals?.errors,
        { isErrorMetric: true }
      ),
    [kpiData.errorRequests, kpiData.rejectedRequests, prevTotals]
  );

  // Retention metadata from timeseries response
  const retentionLimited = Boolean(
    timeseriesQuery.data?.retention_limited || breakdownQuery.data?.retention_limited
  );
  const earliestRetainedAt =
    timeseriesQuery.data?.earliest_retained_at ||
    breakdownQuery.data?.earliest_retained_at ||
    null;
  const latestRetainedAt =
    timeseriesQuery.data?.latest_retained_at ||
    breakdownQuery.data?.latest_retained_at ||
    null;

  // Breakdown rows and totals
  const breakdownRows = breakdownQuery.data?.rows || [];
  const breakdownOther = breakdownQuery.data?.other || {
    id: "other",
    name: "Other",
    is_unknown: false,
    is_deleted: false,
    total_requests: 0,
    successful_requests: 0,
    error_requests: 0,
    rejected_requests: 0,
    input_tokens: null,
    cached_input_tokens: null,
    output_tokens: null,
    cost_micros: null,
  };
  const breakdownTotal = breakdownQuery.data?.total || {
    total_requests: 0,
    successful_requests: 0,
    error_requests: 0,
    rejected_requests: 0,
    input_tokens: null,
    cached_input_tokens: null,
    output_tokens: null,
    cost_micros: null,
  };

  // Error boundary checks
  const error = timeseriesQuery.error || breakdownQuery.error;
  const is401 = error instanceof AdminApiError && error.status === 401;
  const is503 = error instanceof AdminApiError && error.status === 503;

  return (
    <div className="gw-page-content gw-usage-container" data-testid="usage-page">
      {/* 1. Range & Resolution Controls */}
      <UsageControls
        preset={activePreset}
        onPresetChange={handlePresetChange}
        bucket={bucketParam}
        onBucketChange={handleBucketChange}
        customFrom={fromParam}
        customTo={toParam}
        onCustomRangeApply={handleCustomRangeApply}
        isFetching={isFetching}
        onRefresh={handleRefresh}
      />

      {/* 2. Error and Alert States */}
      {!isOnline && (
        <Alert
          variant="warning"
          icon={<WifiOff size={18} />}
          title="Network Connection Offline"
          data-testid="usage-offline-alert"
        >
          You are currently offline. Displayed analytics data may be outdated.
        </Alert>
      )}

      {is401 && (
        <Alert
          variant="danger"
          title="Session Expired"
          icon={<LogIn size={16} />}
          action={
            <Link to="/login" style={{ textDecoration: "none" }}>
              <Button size="sm" variant="secondary">
                Sign In
              </Button>
            </Link>
          }
          data-testid="usage-401-alert"
        >
          Your administrative session has expired. Please{" "}
          <Link to="/login" style={{ color: "inherit", textDecoration: "underline" }}>
            log in again
          </Link>{" "}
          to access telemetry data.
        </Alert>
      )}

      {is503 && (
        <Alert
          variant="warning"
          title="Gateway Analytics Capacity Constrained"
          icon={<AlertTriangle size={16} />}
        >
          Usage query concurrency limit reached. The gateway is prioritizing proxy transport. Please
          retry in a few moments.
          <div style={{ marginTop: "8px" }}>
            <Button size="sm" variant="outline" onClick={handleRefresh}>
              <RefreshCw size={13} style={{ marginRight: "4px" }} />
              Retry Query
            </Button>
          </div>
        </Alert>
      )}

      {error && !is401 && !is503 && (
        <Alert
          variant="danger"
          title="Unable to Load Usage Analytics"
          icon={<AlertTriangle size={16} />}
        >
          {error.message || "An unexpected error occurred while loading analytics telemetry."}
          <div style={{ marginTop: "8px" }}>
            <Button size="sm" variant="outline" onClick={handleRefresh}>
              <RefreshCw size={13} style={{ marginRight: "4px" }} />
              Retry Query
            </Button>
          </div>
        </Alert>
      )}

      {/* 3. Retention Notice */}
      <RetentionNotice
        preset={activePreset}
        retentionLimited={retentionLimited}
        earliestRetainedAt={earliestRetainedAt}
        latestRetainedAt={latestRetainedAt}
      />

      {/* 4. <= 5 KPI Cards (Summary) */}
      {timeseriesQuery.isLoading && !timeseriesQuery.data ? (
        <div className="gw-usage-kpi-grid">
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
        </div>
      ) : (
        <UsageKpiCards
          data={kpiData}
          requestDelta={requestDelta}
          tokenDelta={tokenDelta}
          costDelta={costDelta}
          errorDelta={errorDelta}
        />
      )}

      {/* 5. Primary Chart Card (Selected Metric Only) */}
      <LazyChartCard
        buckets={timeseriesQuery.data?.buckets || []}
        activeMetric={activeMetric}
        onMetricChange={handleMetricChange}
        isLoading={timeseriesQuery.isLoading}
      />

      {/* 6. Secondary Rankings Card */}
      <RankingsCard
        rows={breakdownRows}
        other={breakdownOther}
        total={breakdownTotal}
        dimension={activeRankDim}
        onDimensionChange={handleRankDimChange}
        metric={activeRankMetric}
        onMetricChange={handleRankMetricChange}
        isLoading={breakdownQuery.isLoading}
      />
    </div>
  );
};

export default UsagePage;
