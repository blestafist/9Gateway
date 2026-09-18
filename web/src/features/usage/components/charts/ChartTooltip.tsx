import React from "react";
import { formatTimestamp } from "../../../../shared/formatters";

export interface TooltipPayloadItem {
  name: string;
  value: number | null | undefined;
  color?: string;
  dataKey?: string;
  payload?: Record<string, unknown>;
}

export interface ChartTooltipProps {
  active?: boolean;
  payload?: TooltipPayloadItem[];
  label?: string;
  metricType?: "requests" | "tokens" | "cost" | "latency";
}

export const ChartTooltip: React.FC<ChartTooltipProps> = ({
  active,
  payload,
  label,
  metricType = "requests",
}) => {
  if (!active || !payload || payload.length === 0) {
    return null;
  }

  const rawPoint = payload[0]?.payload as
    | { startIso?: string; endIso?: string; [key: string]: unknown }
    | undefined;

  const startText = rawPoint?.startIso ? formatTimestamp(rawPoint.startIso) : label;
  const endText = rawPoint?.endIso ? formatTimestamp(rawPoint.endIso) : undefined;

  return (
    <div
      className="gw-chart-tooltip"
      role="tooltip"
      aria-hidden="false"
      style={{
        backgroundColor: "var(--bg-surface-2, #1c1e27)",
        border: "1px solid var(--border-default, #2a2d3b)",
        borderRadius: "var(--radius-md, 6px)",
        padding: "8px 12px",
        boxShadow: "var(--shadow-md, 0 4px 12px rgba(0,0,0,0.5))",
        fontSize: "var(--font-size-xs, 12px)",
        color: "var(--text-primary, #f8fafc)",
        pointerEvents: "none",
        minWidth: "180px",
        maxWidth: "280px",
        zIndex: 50,
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border-subtle, #1f222d)",
          paddingBottom: "4px",
          marginBottom: "6px",
          color: "var(--text-secondary, #94a3b8)",
          fontFamily: "var(--font-mono, monospace)",
          fontSize: "11px",
        }}
      >
        <div>{startText}</div>
        {endText && <div style={{ opacity: 0.8 }}>to {endText}</div>}
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
        {payload.map((item, idx) => {
          const val = item.value;
          let formattedValue = "—";

          if (val !== null && val !== undefined) {
            if (metricType === "cost") {
              formattedValue = `$${Number(val).toFixed(4)}`;
            } else if (metricType === "latency") {
              formattedValue = `${Number(val).toFixed(1)} ms`;
            } else {
              formattedValue = Number(val).toLocaleString();
            }
          } else {
            formattedValue = metricType === "cost" ? "Not tracked" : "No samples (gap)";
          }

          let sampleInfo: string | null = null;
          if (metricType === "latency" && rawPoint) {
            if (item.dataKey === "totalMs") {
              sampleInfo = `${rawPoint.totalSamples ?? 0} samples`;
            } else if (item.dataKey === "ttfbMs") {
              sampleInfo = `${rawPoint.ttfbSamples ?? 0} samples`;
            } else if (item.dataKey === "upstreamMs") {
              sampleInfo = `${rawPoint.upstreamSamples ?? 0} samples`;
            }
          }

          return (
            <div
              key={idx}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "8px",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                <span
                  style={{
                    width: "8px",
                    height: "8px",
                    borderRadius: "50%",
                    backgroundColor: item.color || "var(--accent-primary)",
                    display: "inline-block",
                    flexShrink: 0,
                  }}
                />
                <span style={{ color: "var(--text-secondary)" }}>{item.name}:</span>
              </div>
              <div style={{ textAlign: "right", fontFamily: "var(--font-mono)" }}>
                <span style={{ fontWeight: 600 }}>{formattedValue}</span>
                {sampleInfo && (
                  <span
                    style={{
                      display: "block",
                      fontSize: "10px",
                      color: "var(--text-muted)",
                      fontWeight: 400,
                    }}
                  >
                    ({sampleInfo})
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};
