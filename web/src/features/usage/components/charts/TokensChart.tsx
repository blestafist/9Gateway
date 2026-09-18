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
import { TokensChartPoint } from "./chartTypes";
import { ChartTooltip } from "./ChartTooltip";
import { formatTokenCount } from "../../../../shared/formatters";

export interface TokensChartProps {
  data: TokensChartPoint[];
  reducedMotion?: boolean;
}

export const TokensChart: React.FC<TokensChartProps> = ({ data, reducedMotion = false }) => {
  return (
    <div className="gw-chart-wrapper" data-testid="tokens-chart">
      <ResponsiveContainer width="100%" height={320}>
        <AreaChart data={data} margin={{ top: 10, right: 15, left: -10, bottom: 0 }}>
          <defs>
            <linearGradient id="token-in-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--metric-tokens-in, #f06a4b)" stopOpacity={0.4} />
              <stop offset="95%" stopColor="var(--metric-tokens-in, #f06a4b)" stopOpacity={0.05} />
            </linearGradient>
            <linearGradient id="token-cached-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--metric-tokens-cache, #3b82f6)" stopOpacity={0.4} />
              <stop offset="95%" stopColor="var(--metric-tokens-cache, #3b82f6)" stopOpacity={0.05} />
            </linearGradient>
            <linearGradient id="token-out-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--metric-tokens-out, #10b981)" stopOpacity={0.4} />
              <stop offset="95%" stopColor="var(--metric-tokens-out, #10b981)" stopOpacity={0.05} />
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
            tickFormatter={(val) => formatTokenCount(val, true)}
          />
          <Tooltip content={<ChartTooltip metricType="tokens" />} />
          <Legend
            verticalAlign="top"
            align="right"
            iconType="circle"
            wrapperStyle={{ paddingBottom: "12px", fontSize: "12px" }}
          />
          <Area
            type="monotone"
            dataKey="cached"
            name="Cached Tokens"
            stackId="tokens"
            stroke="var(--metric-tokens-cache, #3b82f6)"
            fill="url(#token-cached-grad)"
            strokeWidth={1.5}
            isAnimationActive={!reducedMotion}
          />
          <Area
            type="monotone"
            dataKey="input"
            name="Input Tokens"
            stackId="tokens"
            stroke="var(--metric-tokens-in, #f06a4b)"
            fill="url(#token-in-grad)"
            strokeWidth={1.5}
            isAnimationActive={!reducedMotion}
          />
          <Area
            type="monotone"
            dataKey="output"
            name="Output Tokens"
            stackId="tokens"
            stroke="var(--metric-tokens-out, #10b981)"
            fill="url(#token-out-grad)"
            strokeWidth={1.5}
            isAnimationActive={!reducedMotion}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
};
