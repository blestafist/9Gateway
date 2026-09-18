import React from "react";
import { UsageTimeseriesBucket } from "../../types";
import {
  formatTimestamp,
  formatTokenCount,
  formatCostMicros,
} from "../../../../shared/formatters";
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "../../../../shared/ui";

export interface ChartDataTableProps {
  buckets: UsageTimeseriesBucket[];
  metric: "requests" | "tokens" | "cost" | "latency";
}

export const ChartDataTable: React.FC<ChartDataTableProps> = ({ buckets, metric }) => {
  if (buckets.length === 0) {
    return (
      <div style={{ padding: "16px", textAlign: "center", color: "var(--text-muted)" }}>
        No bucket data available.
      </div>
    );
  }

  return (
    <div
      tabIndex={0}
      role="region"
      aria-label={`${metric} data table view`}
      style={{
        maxHeight: "360px",
        overflowY: "auto",
        border: "1px solid var(--border-subtle)",
        borderRadius: "var(--radius-md)",
      }}
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Time Window (UTC)</TableHead>
            {metric === "requests" && (
              <>
                <TableHead>Total Requests</TableHead>
                <TableHead>Successful</TableHead>
                <TableHead>Error</TableHead>
                <TableHead>Rejected</TableHead>
                <TableHead>Success Rate</TableHead>
              </>
            )}
            {metric === "tokens" && (
              <>
                <TableHead>Input Tokens</TableHead>
                <TableHead>Cached Tokens</TableHead>
                <TableHead>Output Tokens</TableHead>
                <TableHead>Total Tokens</TableHead>
                <TableHead>Cache Rate</TableHead>
              </>
            )}
            {metric === "cost" && (
              <>
                <TableHead>Estimated Cost</TableHead>
                <TableHead>Status</TableHead>
              </>
            )}
            {metric === "latency" && (
              <>
                <TableHead>Avg Total (ms)</TableHead>
                <TableHead>Avg TTFB (ms)</TableHead>
                <TableHead>Avg Upstream (ms)</TableHead>
                <TableHead>Samples</TableHead>
              </>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {buckets.map((b, idx) => {
            const timeStr = `${formatTimestamp(b.bucket_start)} – ${formatTimestamp(b.bucket_end)}`;

            return (
              <TableRow key={idx}>
                <TableCell style={{ fontFamily: "var(--font-mono)", fontSize: "12px" }}>
                  {timeStr}
                </TableCell>

                {metric === "requests" && (
                  <>
                    <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 600 }}>
                      {b.total_requests.toLocaleString()}
                    </TableCell>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color: "var(--status-success)",
                      }}
                    >
                      {b.successful_requests.toLocaleString()}
                    </TableCell>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color:
                          b.error_requests > 0
                            ? "var(--status-danger)"
                            : "var(--text-muted)",
                      }}
                    >
                      {b.error_requests.toLocaleString()}
                    </TableCell>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color:
                          b.rejected_requests > 0
                            ? "var(--status-warning)"
                            : "var(--text-muted)",
                      }}
                    >
                      {b.rejected_requests.toLocaleString()}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {b.total_requests > 0
                        ? `${((b.successful_requests / b.total_requests) * 100).toFixed(1)}%`
                        : "—"}
                    </TableCell>
                  </>
                )}

                {metric === "tokens" && (
                  <>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color: "var(--metric-tokens-in)",
                      }}
                    >
                      {formatTokenCount(b.input_tokens)}
                    </TableCell>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color: "var(--metric-tokens-cache)",
                      }}
                    >
                      {formatTokenCount(b.cached_input_tokens)}
                    </TableCell>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color: "var(--metric-tokens-out)",
                      }}
                    >
                      {formatTokenCount(b.output_tokens)}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 600 }}>
                      {formatTokenCount(b.input_tokens + b.output_tokens)}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {b.input_tokens > 0
                        ? `${((b.cached_input_tokens / b.input_tokens) * 100).toFixed(1)}%`
                        : "—"}
                    </TableCell>
                  </>
                )}

                {metric === "cost" && (
                  <>
                    <TableCell
                      style={{
                        fontFamily: "var(--font-mono)",
                        color: "var(--metric-cost)",
                        fontWeight: 600,
                      }}
                    >
                      {b.cost_micros !== null ? formatCostMicros(b.cost_micros) : "—"}
                    </TableCell>
                    <TableCell style={{ color: "var(--text-secondary)" }}>
                      {b.cost_micros !== null ? "Estimated" : "Not tracked (null)"}
                    </TableCell>
                  </>
                )}

                {metric === "latency" && (
                  <>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {b.total_latency_samples > 0 && b.avg_total_latency_micros !== null
                        ? `${(b.avg_total_latency_micros / 1000).toFixed(1)} ms`
                        : "— (gap)"}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {b.ttfb_latency_samples > 0 && b.avg_ttfb_latency_micros !== null
                        ? `${(b.avg_ttfb_latency_micros / 1000).toFixed(1)} ms`
                        : "— (gap)"}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {b.upstream_latency_samples > 0 && b.avg_upstream_latency_micros !== null
                        ? `${(b.avg_upstream_latency_micros / 1000).toFixed(1)} ms`
                        : "— (gap)"}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {b.total_latency_samples}
                    </TableCell>
                  </>
                )}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
};
