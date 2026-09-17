import React, { useState } from "react";
import { Card, CardHeader, CardTitle, CardContent, Tabs, EmptyState } from "../../shared/ui";
import { BarChart3 } from "lucide-react";

const RANGE_TABS = [
  { id: "today", label: "Today" },
  { id: "24h", label: "24h" },
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
  { id: "90d", label: "90d" },
  { id: "all", label: "All retained" },
];

const METRIC_TABS = [
  { id: "tokens", label: "Tokens" },
  { id: "cost", label: "Cost" },
  { id: "requests", label: "Requests" },
];

export const UsagePage: React.FC = () => {
  const [activeRange, setActiveRange] = useState("24h");
  const [activeMetric, setActiveMetric] = useState("tokens");

  return (
    <div className="gw-page-content" data-testid="usage-page">
      {/* Control Bar: Range Tabs & Metric Tabs */}
      <div className="gw-usage-controls">
        <div className="gw-usage-controls-group">
          <span className="gw-control-label">Range:</span>
          <Tabs
            items={RANGE_TABS}
            activeTab={activeRange}
            onChange={setActiveRange}
            aria-label="Time range"
          />
        </div>
        <div className="gw-usage-controls-group">
          <span className="gw-control-label">Metric:</span>
          <Tabs
            items={METRIC_TABS}
            activeTab={activeMetric}
            onChange={setActiveMetric}
            aria-label="Metric view"
          />
        </div>
      </div>

      {/* Primary Chart Placeholder Card */}
      <Card className="gw-chart-card">
        <CardHeader>
          <div className="gw-chart-header">
            <div>
              <CardTitle>Token & Volume Telemetry</CardTitle>
              <p className="gw-card-subtitle">
                Time-series aggregation for range: <strong style={{ color: "var(--accent-primary)" }}>{activeRange}</strong> ({activeMetric})
              </p>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <div className="gw-chart-placeholder-area">
            <EmptyState
              icon={<BarChart3 size={36} />}
              title="Interactive Chart Framing"
              description="SQLite telemetry aggregations will connect in T171. Heavy chart dependencies are omitted from initial shell load."
            />
          </div>
        </CardContent>
      </Card>

      {/* Ranking Card Placeholder */}
      <div className="gw-ranking-grid">
        <Card>
          <CardHeader>
            <CardTitle>Top Models by Consumption</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="gw-ranking-list">
              <div className="gw-ranking-row">
                <span className="gw-ranking-name">claude-sonnet-5</span>
                <span className="gw-ranking-metric" style={{ color: "var(--metric-tokens-in)" }}>12.4M tokens</span>
                <span className="gw-ranking-cost" style={{ color: "var(--metric-cost)" }}>$18.60</span>
              </div>
              <div className="gw-ranking-row">
                <span className="gw-ranking-name">gpt-luna</span>
                <span className="gw-ranking-metric" style={{ color: "var(--metric-tokens-in)" }}>5.2M tokens</span>
                <span className="gw-ranking-cost" style={{ color: "var(--metric-cost)" }}>$7.80</span>
              </div>
              <div className="gw-ranking-row">
                <span className="gw-ranking-name">gemini-3.8-flash</span>
                <span className="gw-ranking-metric" style={{ color: "var(--metric-tokens-in)" }}>1.5M tokens</span>
                <span className="gw-ranking-cost" style={{ color: "var(--metric-cost)" }}>$1.59</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export default UsagePage;
