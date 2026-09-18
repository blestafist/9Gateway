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
import { RequestsChartPoint } from "./chartTypes";
import { ChartTooltip } from "./ChartTooltip";

export interface RequestsChartProps {
  data: RequestsChartPoint[];
  reducedMotion?: boolean;
}

export const RequestsChart: React.FC<RequestsChartProps> = ({ data, reducedMotion = false }) => {
  return (
    <div className="gw-chart-wrapper" data-testid="requests-chart">
      <ResponsiveContainer width="100%" height={320}>
        <AreaChart data={data} margin={{ top: 10, right: 15, left: -10, bottom: 0 }}>
          <defs>
            <linearGradient id="req-success-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--status-success, #10b981)" stopOpacity={0.25} />
              <stop offset="95%" stopColor="var(--status-success, #10b981)" stopOpacity={0.0} />
            </linearGradient>
            <linearGradient id="req-error-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--status-danger, #ef4444)" stopOpacity={0.25} />
              <stop offset="95%" stopColor="var(--status-danger, #ef4444)" stopOpacity={0.0} />
            </linearGradient>
            <linearGradient id="req-reject-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--status-warning, #f59e0b)" stopOpacity={0.25} />
              <stop offset="95%" stopColor="var(--status-warning, #f59e0b)" stopOpacity={0.0} />
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
            tickFormatter={(val) => Number(val).toLocaleString()}
          />
          <Tooltip content={<ChartTooltip metricType="requests" />} />
          <Legend
            verticalAlign="top"
            align="right"
            iconType="circle"
            wrapperStyle={{ paddingBottom: "12px", fontSize: "12px" }}
          />
          <Area
            type="monotone"
            dataKey="successful"
            name="Successful (Solid)"
            stroke="var(--status-success, #10b981)"
            fill="url(#req-success-grad)"
            strokeWidth={2}
            isAnimationActive={!reducedMotion}
          />
          <Area
            type="monotone"
            dataKey="error"
            name="Errors (Dashed)"
            stroke="var(--status-danger, #ef4444)"
            strokeDasharray="4 4"
            fill="url(#req-error-grad)"
            strokeWidth={2}
            isAnimationActive={!reducedMotion}
          />
          <Area
            type="monotone"
            dataKey="rejected"
            name="Rejected (Dotted)"
            stroke="var(--status-warning, #f59e0b)"
            strokeDasharray="2 2"
            fill="url(#req-reject-grad)"
            strokeWidth={2}
            isAnimationActive={!reducedMotion}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
};
