import React from "react";
import {
  ResponsiveContainer,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from "recharts";
import { LatencyChartPoint } from "./chartTypes";
import { ChartTooltip } from "./ChartTooltip";
import { Alert } from "../../../../shared/ui";

export interface LatencyChartProps {
  data: LatencyChartPoint[];
  reducedMotion?: boolean;
}

export const LatencyChart: React.FC<LatencyChartProps> = ({ data, reducedMotion = false }) => {
  const hasAnySamples = data.some((p) => p.totalSamples > 0 || p.ttfbSamples > 0 || p.upstreamSamples > 0);

  return (
    <div className="gw-chart-wrapper" data-testid="latency-chart">
      {!hasAnySamples && (
        <div style={{ marginBottom: "12px" }}>
          <Alert variant="info" title="Latency Telemetry">
            No completed requests with latency samples were recorded in this range. Intervals
            without samples are rendered as gaps, not zeros.
          </Alert>
        </div>
      )}

      <ResponsiveContainer width="100%" height={320}>
        <LineChart data={data} margin={{ top: 10, right: 15, left: -10, bottom: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border-subtle, #1f222d)" vertical={false} />
          <XAxis
            dataKey="timeLabel"
            stroke="var(--text-muted, #64748b)"
            fontSize={11}
            tickLine={false}
            axisLine={{ stroke: "var(--border-subtle, #1f222d)" }}
          />
          <YAxis
            stroke="var(--text-muted, #64748b)"
            fontSize={11}
            tickLine={false}
            axisLine={{ stroke: "var(--border-subtle, #1f222d)" }}
            tickFormatter={(val) => (val !== null && val !== undefined ? `${Number(val).toFixed(0)} ms` : "0 ms")}
          />
          <Tooltip content={<ChartTooltip metricType="latency" />} />
          <Legend
            verticalAlign="top"
            align="right"
            wrapperStyle={{ paddingBottom: "12px", fontSize: "12px" }}
          />
          <Line
            type="monotone"
            dataKey="totalMs"
            name="Total Latency (Solid)"
            stroke="var(--status-info, #3b82f6)"
            strokeWidth={2}
            connectNulls={false}
            dot={{ r: 3, strokeWidth: 1 }}
            isAnimationActive={!reducedMotion}
          />
          <Line
            type="monotone"
            dataKey="ttfbMs"
            name="TTFB Latency (Dashed)"
            stroke="#8b5cf6"
            strokeDasharray="5 5"
            strokeWidth={2}
            connectNulls={false}
            dot={{ r: 3, strokeWidth: 1 }}
            isAnimationActive={!reducedMotion}
          />
          <Line
            type="monotone"
            dataKey="upstreamMs"
            name="Upstream Latency (Dotted)"
            stroke="var(--status-warning, #f59e0b)"
            strokeDasharray="2 2"
            strokeWidth={2}
            connectNulls={false}
            dot={{ r: 3, strokeWidth: 1 }}
            isAnimationActive={!reducedMotion}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
};
