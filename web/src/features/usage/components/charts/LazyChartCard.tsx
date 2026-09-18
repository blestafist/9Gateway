import React, { useState, useMemo, useEffect } from "react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Tabs,
  Button,
  EmptyState,
  Skeleton,
} from "../../../../shared/ui";
import {
  UsageTimeseriesBucket,
  UsageChartMetric,
} from "../../types";
import { transformTimeseriesBuckets } from "./chartTypes";
import { RequestsChart } from "./RequestsChart";
import { TokensChart } from "./TokensChart";
import { CostChart } from "./CostChart";
import { LatencyChart } from "./LatencyChart";
import { ChartDataTable } from "./ChartDataTable";
import { formatTokenCount, formatCostMicros } from "../../../../shared/formatters";
import { BarChart3, Table as TableIcon, Activity } from "lucide-react";

export interface LazyChartCardProps {
  buckets: UsageTimeseriesBucket[];
  activeMetric: UsageChartMetric;
  onMetricChange: (metric: UsageChartMetric) => void;
  isLoading?: boolean;
}

const METRIC_TABS = [
  { id: "requests", label: "Requests" },
  { id: "tokens", label: "Tokens" },
  { id: "cost", label: "Cost" },
  { id: "latency", label: "Latency" },
];

export const LazyChartCard: React.FC<LazyChartCardProps> = ({
  buckets,
  activeMetric,
  onMetricChange,
  isLoading = false,
}) => {
  const [viewMode, setViewMode] = useState<"chart" | "table">("chart");
  const [reducedMotion, setReducedMotion] = useState<boolean>(false);

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    setReducedMotion(mq.matches);
    const handler = (e: MediaQueryListEvent) => setReducedMotion(e.matches);
    mq.addEventListener?.("change", handler);
    return () => mq.removeEventListener?.("change", handler);
  }, []);

  const chartData = useMemo(() => {
    return transformTimeseriesBuckets(buckets);
  }, [buckets]);

  // Screen reader concise summary
  const screenReaderSummary = useMemo(() => {
    if (buckets.length === 0) return "No bucket telemetry available for this range.";
    const totalRequests = buckets.reduce((acc, b) => acc + b.total_requests, 0);
    const totalInput = buckets.reduce((acc, b) => acc + b.input_tokens, 0);
    const totalOutput = buckets.reduce((acc, b) => acc + b.output_tokens, 0);
    const totalCost = buckets.reduce(
      (acc, b) => (b.cost_micros !== null ? (acc ?? 0) + b.cost_micros : acc),
      null as number | null
    );

    return `Time series chart displaying ${buckets.length} time buckets. Total requests: ${totalRequests.toLocaleString()}. Input tokens: ${formatTokenCount(
      totalInput
    )}, output tokens: ${formatTokenCount(totalOutput)}. Total estimated cost: ${
      totalCost !== null ? formatCostMicros(totalCost) : "unavailable"
    }. Currently displaying ${activeMetric} metric view.`;
  }, [buckets, activeMetric]);

  const isEmpty = buckets.length === 0;

  return (
    <Card className="gw-chart-card" data-testid="primary-chart-card">
      <CardHeader>
        <div className="gw-chart-card-header">
          <div>
            <CardTitle>Telemetry & Trends</CardTitle>
            <p className="gw-card-subtitle">
              Interactive time-series aggregation. Switch views to inspect requests, tokens, cost,
              or latencies.
            </p>
          </div>
          <div className="gw-chart-card-actions">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setViewMode(viewMode === "chart" ? "table" : "chart")}
              aria-label={viewMode === "chart" ? "Switch to data table view" : "Switch to chart view"}
            >
              {viewMode === "chart" ? (
                <>
                  <TableIcon size={14} style={{ marginRight: "6px" }} aria-hidden="true" />
                  View as Table
                </>
              ) : (
                <>
                  <BarChart3 size={14} style={{ marginRight: "6px" }} aria-hidden="true" />
                  View as Chart
                </>
              )}
            </Button>
          </div>
        </div>

        {/* Metric Selector Tabs */}
        <div style={{ marginTop: "12px" }}>
          <Tabs
            items={METRIC_TABS}
            activeTab={activeMetric}
            onChange={(tabId) => onMetricChange(tabId as UsageChartMetric)}
            aria-label="Metric view"
          />
        </div>
      </CardHeader>

      <CardContent>
        {/* Concise screen reader summary */}
        <div className="gw-sr-only" aria-live="polite">
          {screenReaderSummary}
        </div>

        {isLoading && isEmpty ? (
          <div style={{ padding: "20px 0" }}>
            <Skeleton variant="rect" height={320} />
          </div>
        ) : isEmpty ? (
          <div style={{ padding: "40px 0" }}>
            <EmptyState
              icon={<Activity size={36} />}
              title="No Activity in Range"
              description="No completed or rejected requests were recorded during this period."
            />
          </div>
        ) : viewMode === "table" ? (
          <ChartDataTable buckets={buckets} metric={activeMetric} />
        ) : (
          <div>
            {/* Only mount and render the selected chart */}
            {activeMetric === "requests" && (
              <RequestsChart data={chartData.requests} reducedMotion={reducedMotion} />
            )}
            {activeMetric === "tokens" && (
              <TokensChart data={chartData.tokens} reducedMotion={reducedMotion} />
            )}
            {activeMetric === "cost" && (
              <CostChart data={chartData.cost} reducedMotion={reducedMotion} />
            )}
            {activeMetric === "latency" && (
              <LatencyChart data={chartData.latency} reducedMotion={reducedMotion} />
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
};

export default LazyChartCard;
