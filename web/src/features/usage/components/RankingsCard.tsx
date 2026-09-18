import React, { useState, useMemo } from "react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Tabs,
  Button,
  Badge,
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableRow,
  TableHead,
  TableCell,
  EmptyState,
  Skeleton,
} from "../../../shared/ui";
import {
  UsageBreakdownRow,
  UsageBreakdownTotal,
  UsageRankingDimension,
  UsageRankingMetric,
} from "../types";
import {
  formatTokenCount,
  formatCostMicros,
} from "../../../shared/formatters";
import { BarChart3, Table as TableIcon, Layers, ArrowUpDown } from "lucide-react";

export interface RankingsCardProps {
  rows: UsageBreakdownRow[];
  other: UsageBreakdownRow;
  total: UsageBreakdownTotal;
  dimension: UsageRankingDimension;
  onDimensionChange: (dim: UsageRankingDimension) => void;
  metric: UsageRankingMetric;
  onMetricChange: (metric: UsageRankingMetric) => void;
  isLoading?: boolean;
}

const DIMENSION_TABS = [
  { id: "model", label: "Models" },
  { id: "key", label: "API Keys" },
  { id: "outcome", label: "Outcomes" },
];

const METRIC_TABS = [
  { id: "requests", label: "Requests" },
  { id: "tokens", label: "Tokens" },
  { id: "cost", label: "Cost" },
];

type SortField = "name" | "metric" | "requests" | "share";
type SortDir = "asc" | "desc";

export const RankingsCard: React.FC<RankingsCardProps> = ({
  rows,
  other,
  total,
  dimension,
  onDimensionChange,
  metric,
  onMetricChange,
  isLoading = false,
}) => {
  const [viewMode, setViewMode] = useState<"bars" | "table">("bars");
  const [sortField, setSortField] = useState<SortField>("metric");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  // Helper to extract metric value for a row
  const getRowMetricValue = (row: UsageBreakdownRow, m: UsageRankingMetric): number | null => {
    switch (m) {
      case "requests":
        return row.total_requests;
      case "tokens":
        return row.input_tokens !== null && row.output_tokens !== null
          ? row.input_tokens + row.output_tokens
          : null;
      case "cost":
        return row.cost_micros;
      default:
        return row.total_requests;
    }
  };

  const getTotalMetricValue = (tot: UsageBreakdownTotal, m: UsageRankingMetric): number | null => {
    switch (m) {
      case "requests":
        return tot.total_requests;
      case "tokens":
        return tot.input_tokens !== null || tot.output_tokens !== null
          ? (tot.input_tokens ?? 0) + (tot.output_tokens ?? 0)
          : null;
      case "cost":
        return tot.cost_micros;
      default:
        return tot.total_requests;
    }
  };

  const totalMetricVal = getTotalMetricValue(total, metric);

  const formatMetricDisplay = (val: number | null, m: UsageRankingMetric): string => {
    if (val === null) return "Unavailable";
    switch (m) {
      case "requests":
        return val.toLocaleString();
      case "tokens":
        return formatTokenCount(val);
      case "cost":
        return formatCostMicros(val);
      default:
        return val.toLocaleString();
    }
  };

  const hasOther =
    other.total_requests > 0 ||
    (other.input_tokens !== null && other.input_tokens > 0) ||
    (other.output_tokens !== null && other.output_tokens > 0) ||
    (other.cost_micros !== null && other.cost_micros > 0);

  // Combine rows with share calculations
  const processedRows = useMemo(() => {
    const list = rows.map((r) => {
      const val = getRowMetricValue(r, metric);
      let share = 0;
      if (totalMetricVal !== null && totalMetricVal > 0 && val !== null && val > 0) {
        share = (val / totalMetricVal) * 100;
      }
      return {
        ...r,
        metricValue: val,
        share,
        isOther: false,
      };
    });

    if (hasOther) {
      const otherVal = getRowMetricValue(other, metric);
      let otherShare = 0;
      if (totalMetricVal !== null && totalMetricVal > 0 && otherVal !== null && otherVal > 0) {
        otherShare = (otherVal / totalMetricVal) * 100;
      }
      list.push({
        ...other,
        id: "other_aggregate",
        name: "Other (aggregated)",
        metricValue: otherVal,
        share: otherShare,
        isOther: true,
      });
    }

    return list;
  }, [rows, other, metric, totalMetricVal, hasOther]);

  // Sort rows for table view
  const sortedRows = useMemo(() => {
    return [...processedRows].sort((a, b) => {
      // Keep other at the bottom if desired, or sort by field
      let comp = 0;
      if (sortField === "name") {
        comp = a.name.localeCompare(b.name);
      } else if (sortField === "requests") {
        comp = a.total_requests - b.total_requests;
      } else if (sortField === "share") {
        comp = a.share - b.share;
      } else {
        const valA = a.metricValue ?? -1;
        const valB = b.metricValue ?? -1;
        comp = valA - valB;
      }
      return sortDir === "asc" ? comp : -comp;
    });
  }, [processedRows, sortField, sortDir]);

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDir(sortDir === "asc" ? "desc" : "asc");
    } else {
      setSortField(field);
      setSortDir("desc");
    }
  };

  const isEmpty = rows.length === 0 && !hasOther;

  if (isLoading && isEmpty) {
    return (
      <Card className="gw-rankings-card" data-testid="rankings-card">
        <CardHeader>
          <CardTitle>Breakdown & Rankings</CardTitle>
        </CardHeader>
        <CardContent>
          <div style={{ padding: "20px 0" }}>
            <Skeleton variant="rect" height={260} />
          </div>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="gw-rankings-card" data-testid="rankings-card">
      <CardHeader>
        <div className="gw-rankings-header">
          <div>
            <CardTitle>Breakdown & Rankings</CardTitle>
            <p className="gw-card-subtitle">
              Top members are selected by request count; this view shows their {metric} share and value. Bounded aggregates cover the remaining members.
            </p>
          </div>
          <div className="gw-rankings-header-actions">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setViewMode(viewMode === "bars" ? "table" : "bars")}
              aria-label={viewMode === "bars" ? "Switch to detailed table view" : "Switch to horizontal bar view"}
            >
              {viewMode === "bars" ? (
                <>
                  <TableIcon size={14} style={{ marginRight: "6px" }} aria-hidden="true" />
                  View as Table
                </>
              ) : (
                <>
                  <BarChart3 size={14} style={{ marginRight: "6px" }} aria-hidden="true" />
                  View as Bars
                </>
              )}
            </Button>
          </div>
        </div>

        {/* Dual Tab Controls: Dimension & Metric */}
        <div className="gw-rankings-controls-row">
          <div className="gw-rankings-dim-tabs">
            <Tabs
              items={DIMENSION_TABS}
              activeTab={dimension}
              onChange={(tabId) => onDimensionChange(tabId as UsageRankingDimension)}
              aria-label="Ranking dimension"
            />
          </div>
          <div className="gw-rankings-metric-tabs">
            <span className="gw-control-label" style={{ marginRight: "6px" }}>
              Display metric:
            </span>
            <Tabs
              items={METRIC_TABS}
              activeTab={metric}
              onChange={(tabId) => onMetricChange(tabId as UsageRankingMetric)}
              aria-label="Ranking metric"
            />
          </div>
        </div>
      </CardHeader>

      <CardContent>
        {isEmpty ? (
          <div style={{ padding: "36px 0" }}>
            <EmptyState
              icon={<Layers size={36} />}
              title="No Breakdown Data"
              description="No usage records found for this dimension and time window."
            />
          </div>
        ) : viewMode === "bars" ? (
          /* Horizontal Bars View */
          <div className="gw-rankings-bars-container" data-testid="rankings-bars">
            {processedRows.map((row, idx) => {
              const maxVal = processedRows[0]?.metricValue ?? 1;
              const barFillWidth =
                maxVal && maxVal > 0 && row.metricValue
                  ? Math.min(100, Math.max(2, (row.metricValue / maxVal) * 100))
                  : 0;

              return (
                <div
                  key={row.id || idx}
                  className={`gw-ranking-bar-row ${row.isOther ? "gw-ranking-bar-row--other" : ""}`}
                >
                  <div className="gw-ranking-bar-label-group">
                    <span className="gw-ranking-bar-rank">#{idx + 1}</span>
                    <span
                      className="gw-ranking-bar-name"
                      title={row.name || row.id}
                    >
                      {row.name || row.id}
                    </span>
                    {row.is_unknown && (
                      <Badge variant="neutral" size="sm" style={{ marginLeft: "6px" }}>
                        Unknown
                      </Badge>
                    )}
                    {row.is_deleted && (
                      <Badge variant="warning" size="sm" style={{ marginLeft: "6px" }}>
                        Deleted
                      </Badge>
                    )}
                    {row.isOther && (
                      <Badge variant="neutral" size="sm" style={{ marginLeft: "6px" }}>
                        Aggregate
                      </Badge>
                    )}
                  </div>

                  <div className="gw-ranking-bar-track-wrapper">
                    <div className="gw-ranking-bar-track" role="progressbar" aria-valuenow={Math.round(row.share)} aria-valuemin={0} aria-valuemax={100}>
                      <div
                        className="gw-ranking-bar-fill"
                        style={{
                          width: `${barFillWidth}%`,
                          backgroundColor:
                            metric === "tokens"
                              ? "var(--metric-tokens-in)"
                              : metric === "cost"
                              ? "var(--metric-cost)"
                              : "var(--accent-primary)",
                        }}
                      />
                    </div>
                  </div>

                  <div className="gw-ranking-bar-stats">
                    <span className="gw-ranking-bar-share">
                      {row.share.toFixed(1)}%
                    </span>
                    <span className="gw-ranking-bar-metric-val">
                      {formatMetricDisplay(row.metricValue, metric)}
                    </span>
                    <span className="gw-ranking-bar-reqs">
                      ({row.total_requests.toLocaleString()} reqs)
                    </span>
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          /* Detailed Sortable Table View */
          <div
            tabIndex={0}
            role="region"
            aria-label={`${dimension} rankings data table`}
            className="gw-rankings-table-container"
            data-testid="rankings-table"
          >
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <button
                      type="button"
                      className="gw-table-sort-btn"
                      onClick={() => handleSort("name")}
                    >
                      {dimension === "model"
                        ? "Model Name"
                        : dimension === "key"
                        ? "Key Name / ID"
                        : "Outcome"}
                      <ArrowUpDown size={12} style={{ marginLeft: "4px" }} />
                    </button>
                  </TableHead>
                  <TableHead>
                    <button
                      type="button"
                      className="gw-table-sort-btn"
                      onClick={() => handleSort("share")}
                    >
                      Share of Total
                      <ArrowUpDown size={12} style={{ marginLeft: "4px" }} />
                    </button>
                  </TableHead>
                  <TableHead>
                    <button
                      type="button"
                      className="gw-table-sort-btn"
                      onClick={() => handleSort("metric")}
                    >
                      {metric.toUpperCase()} Value
                      <ArrowUpDown size={12} style={{ marginLeft: "4px" }} />
                    </button>
                  </TableHead>
                  <TableHead>
                    <button
                      type="button"
                      className="gw-table-sort-btn"
                      onClick={() => handleSort("requests")}
                    >
                      Total Requests
                      <ArrowUpDown size={12} style={{ marginLeft: "4px" }} />
                    </button>
                  </TableHead>
                  <TableHead>Errors</TableHead>
                  <TableHead>Rejected</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedRows.map((row, idx) => (
                  <TableRow key={row.id || idx}>
                    <TableCell>
                      <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                        <span style={{ fontWeight: 600, fontFamily: "var(--font-mono)", fontSize: "12px" }}>
                          {row.name || row.id}
                        </span>
                        {row.is_unknown && <Badge variant="neutral" size="sm">Unknown</Badge>}
                        {row.is_deleted && <Badge variant="warning" size="sm">Deleted</Badge>}
                        {row.isOther && <Badge variant="neutral" size="sm">Aggregate</Badge>}
                      </div>
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)", fontSize: "12px" }}>
                      {row.share.toFixed(1)}%
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 600 }}>
                      {formatMetricDisplay(row.metricValue, metric)}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)" }}>
                      {row.total_requests.toLocaleString()}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)", color: row.error_requests > 0 ? "var(--status-danger)" : undefined }}>
                      {row.error_requests.toLocaleString()}
                    </TableCell>
                    <TableCell style={{ fontFamily: "var(--font-mono)", color: row.rejected_requests > 0 ? "var(--status-warning)" : undefined }}>
                      {row.rejected_requests.toLocaleString()}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
              <TableFooter>
                <TableRow>
                  <TableCell style={{ fontWeight: 700 }}>Total (untruncated)</TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 700 }}>100.0%</TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 700 }}>
                    {formatMetricDisplay(totalMetricVal, metric)}
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 700 }}>
                    {total.total_requests.toLocaleString()}
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 700 }}>
                    {total.error_requests.toLocaleString()}
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 700 }}>
                    {total.rejected_requests.toLocaleString()}
                  </TableCell>
                </TableRow>
              </TableFooter>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
};
