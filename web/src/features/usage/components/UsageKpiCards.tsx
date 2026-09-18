import React from "react";
import { Card, CardContent } from "../../../shared/ui";
import {
  formatTokenCount,
  formatCostMicros,
  formatDurationMicros,
} from "../../../shared/formatters";
import { UsageDeltaResult } from "../deltas";
import { DeltaBadge } from "./DeltaBadge";
import { Activity, Layers, DollarSign, Clock, AlertTriangle } from "lucide-react";

export interface UsageKpisData {
  totalRequests: number;
  successfulRequests: number;
  errorRequests: number;
  rejectedRequests: number;
  peakBucketRequests: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  totalCostMicros: number | null;
  avgTotalLatencyMicros: number | null;
  totalLatencySamples: number;
  avgTtfbLatencyMicros: number | null;
  avgUpstreamLatencyMicros: number | null;
}

export interface UsageKpiCardsProps {
  data: UsageKpisData;
  requestDelta: UsageDeltaResult;
  tokenDelta: UsageDeltaResult;
  costDelta: UsageDeltaResult;
  errorDelta: UsageDeltaResult;
}

export const UsageKpiCards: React.FC<UsageKpiCardsProps> = ({
  data,
  requestDelta,
  tokenDelta,
  costDelta,
  errorDelta,
}) => {
  const totalTokens = data.inputTokens + data.outputTokens;
  const cacheRate =
    data.inputTokens > 0
      ? ((data.cachedInputTokens / data.inputTokens) * 100).toFixed(1)
      : "0.0";

  const totalErrorsAndRejections = data.errorRequests + data.rejectedRequests;
  const errorRate =
    data.totalRequests > 0
      ? ((totalErrorsAndRejections / data.totalRequests) * 100).toFixed(1)
      : "0.0";

  return (
    <div className="gw-usage-kpi-grid" data-testid="usage-kpi-grid">
      {/* 1. Total Requests */}
      <Card className="gw-kpi-card" data-testid="kpi-requests">
        <CardContent className="gw-kpi-card-content">
          <div className="gw-kpi-header">
            <span className="gw-kpi-title">Total Requests</span>
            <Activity size={16} className="gw-kpi-icon" aria-hidden="true" />
          </div>
          <div className="gw-kpi-value">{data.totalRequests.toLocaleString()}</div>
          <DeltaBadge delta={requestDelta} />
          <div className="gw-kpi-subtitle">
            Peak: {data.peakBucketRequests.toLocaleString()} reqs / bucket
          </div>
        </CardContent>
      </Card>

      {/* 2. Total Tokens */}
      <Card className="gw-kpi-card" data-testid="kpi-tokens">
        <CardContent className="gw-kpi-card-content">
          <div className="gw-kpi-header">
            <span className="gw-kpi-title">Total Tokens</span>
            <Layers size={16} className="gw-kpi-icon" aria-hidden="true" />
          </div>
          <div className="gw-kpi-value" style={{ color: "var(--metric-tokens-in)" }}>
            {formatTokenCount(totalTokens)}
          </div>
          <DeltaBadge delta={tokenDelta} />
          <div className="gw-kpi-subtitle" title={`Input: ${data.inputTokens.toLocaleString()}, Output: ${data.outputTokens.toLocaleString()}, Cached: ${data.cachedInputTokens.toLocaleString()}`}>
            In: {formatTokenCount(data.inputTokens)} · Out: {formatTokenCount(data.outputTokens)} · Cached: {formatTokenCount(data.cachedInputTokens)} ({cacheRate}%)
          </div>
        </CardContent>
      </Card>

      {/* 3. Estimated Cost */}
      <Card className="gw-kpi-card" data-testid="kpi-cost">
        <CardContent className="gw-kpi-card-content">
          <div className="gw-kpi-header">
            <span className="gw-kpi-title">Estimated Cost</span>
            <DollarSign size={16} className="gw-kpi-icon" aria-hidden="true" />
          </div>
          <div className="gw-kpi-value" style={{ color: "var(--metric-cost)" }}>
            {data.totalCostMicros !== null ? formatCostMicros(data.totalCostMicros) : "Unavailable"}
          </div>
          <DeltaBadge delta={costDelta} />
          <div className="gw-kpi-subtitle">
            Est. based on token pricing
          </div>
        </CardContent>
      </Card>

      {/* 4. Average Latency */}
      <Card className="gw-kpi-card" data-testid="kpi-latency">
        <CardContent className="gw-kpi-card-content">
          <div className="gw-kpi-header">
            <span className="gw-kpi-title">Avg Latency</span>
            <Clock size={16} className="gw-kpi-icon" aria-hidden="true" />
          </div>
          <div className="gw-kpi-value">
            {data.avgTotalLatencyMicros !== null
              ? formatDurationMicros(data.avgTotalLatencyMicros)
              : "No data"}
          </div>
          <div className="gw-kpi-subtitle" style={{ marginTop: "4px" }}>
            {data.totalLatencySamples > 0
              ? `${data.totalLatencySamples.toLocaleString()} samples`
              : "Missing samples rendered as gaps"}
          </div>
          <div className="gw-kpi-subtitle">
            TTFB: {data.avgTtfbLatencyMicros !== null ? formatDurationMicros(data.avgTtfbLatencyMicros) : "—"} · Up: {data.avgUpstreamLatencyMicros !== null ? formatDurationMicros(data.avgUpstreamLatencyMicros) : "—"}
          </div>
        </CardContent>
      </Card>

      {/* 5. Errors & Rejections */}
      <Card className="gw-kpi-card" data-testid="kpi-errors">
        <CardContent className="gw-kpi-card-content">
          <div className="gw-kpi-header">
            <span className="gw-kpi-title">Errors & Rejections</span>
            <AlertTriangle
              size={16}
              className="gw-kpi-icon"
              style={{ color: totalErrorsAndRejections > 0 ? "var(--status-warning)" : undefined }}
              aria-hidden="true"
            />
          </div>
          <div
            className="gw-kpi-value"
            style={{
              color: totalErrorsAndRejections > 0 ? "var(--status-warning)" : undefined,
            }}
          >
            {totalErrorsAndRejections.toLocaleString()}
          </div>
          <DeltaBadge delta={errorDelta} />
          <div className="gw-kpi-subtitle">
            {data.errorRequests} errors · {data.rejectedRequests} rejected ({errorRate}%)
          </div>
        </CardContent>
      </Card>
    </div>
  );
};
