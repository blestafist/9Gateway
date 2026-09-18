import React from "react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from "recharts";
import { CostChartPoint } from "./chartTypes";
import { ChartTooltip } from "./ChartTooltip";
import { Alert } from "../../../../shared/ui";

export interface CostChartProps {
  data: CostChartPoint[];
  reducedMotion?: boolean;
}

export const CostChart: React.FC<CostChartProps> = ({ data, reducedMotion = false }) => {
  const allNullCost = data.length > 0 && data.every((p) => p.costDollars === null);

  return (
    <div className="gw-chart-wrapper" data-testid="cost-chart">
      {allNullCost && (
        <div style={{ marginBottom: "12px" }}>
          <Alert variant="info" title="Cost Tracking">
            Cost was not recorded for the requests in this time window. Gaps in the line indicate
            intervals without cost telemetry.
          </Alert>
        </div>
      )}

      <ResponsiveContainer width="100%" height={320}>
        <AreaChart data={data} margin={{ top: 10, right: 15, left: -10, bottom: 0 }}>
          <defs>
            <linearGradient id="cost-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--metric-cost, #f59e0b)" stopOpacity={0.3} />
              <stop offset="95%" stopColor="var(--metric-cost, #f59e0b)" stopOpacity={0.02} />
            </linearGradient>
          </defs>
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
            tickFormatter={(val) => (val !== null && val !== undefined ? `$${Number(val).toFixed(2)}` : "$0")}
          />
          <Tooltip content={<ChartTooltip metricType="cost" />} />
          <Legend
            verticalAlign="top"
            align="right"
            iconType="circle"
            wrapperStyle={{ paddingBottom: "12px", fontSize: "12px" }}
          />
          <Area
            type="monotone"
            dataKey="costDollars"
            name="Estimated Cost ($)"
            stroke="var(--metric-cost, #f59e0b)"
            fill="url(#cost-grad)"
            strokeWidth={2}
            connectNulls={false}
            isAnimationActive={!reducedMotion}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
};
