import React from "react";
import { Card, CardHeader, CardTitle, CardContent, StatusPill, Badge } from "../../shared/ui";
import { Activity, Zap, Database, Server } from "lucide-react";

export const OverviewPage: React.FC = () => {
  return (
    <div className="gw-page-content" data-testid="overview-page">
      {/* KPI Metric Cards Row */}
      <section aria-label="Operations Metrics" className="gw-kpi-grid">
        <Card className="gw-kpi-card">
          <CardContent>
            <div className="gw-kpi-label-row">
              <span className="gw-kpi-label">Total Requests</span>
              <Badge variant="neutral" size="sm">24h</Badge>
            </div>
            <div className="gw-kpi-value" style={{ color: "var(--metric-requests)" }}>
              150
            </div>
            <span className="gw-kpi-subtext">Transparent policy proxying</span>
          </CardContent>
        </Card>

        <Card className="gw-kpi-card">
          <CardContent>
            <div className="gw-kpi-label-row">
              <span className="gw-kpi-label">Total Input Tokens</span>
              <Badge variant="warning" size="sm">Ingest</Badge>
            </div>
            <div className="gw-kpi-value" style={{ color: "var(--metric-tokens-in)" }}>
              19,178,344
            </div>
            <span className="gw-kpi-subtext">Prompt tokens processed</span>
          </CardContent>
        </Card>

        <Card className="gw-kpi-card">
          <CardContent>
            <div className="gw-kpi-label-row">
              <span className="gw-kpi-label">Cached Tokens</span>
              <Badge variant="info" size="sm">Cache</Badge>
            </div>
            <div className="gw-kpi-value" style={{ color: "var(--metric-tokens-cache)" }}>
              11,381,601
            </div>
            <span className="gw-kpi-subtext">59.3% cache efficiency</span>
          </CardContent>
        </Card>

        <Card className="gw-kpi-card">
          <CardContent>
            <div className="gw-kpi-label-row">
              <span className="gw-kpi-label">Output Tokens</span>
              <Badge variant="success" size="sm">Generate</Badge>
            </div>
            <div className="gw-kpi-value" style={{ color: "var(--metric-tokens-out)" }}>
              72,803
            </div>
            <span className="gw-kpi-subtext">Completed generation tokens</span>
          </CardContent>
        </Card>

        <Card className="gw-kpi-card">
          <CardContent>
            <div className="gw-kpi-label-row">
              <span className="gw-kpi-label">Est. Cost</span>
              <Badge variant="warning" size="sm">Est.</Badge>
            </div>
            <div className="gw-kpi-value" style={{ color: "var(--metric-cost)" }}>
              ~$27.99
            </div>
            <span className="gw-kpi-subtext">Estimated provider billing</span>
          </CardContent>
        </Card>
      </section>

      {/* Gateway Status & Operational State */}
      <section aria-label="System Status" className="gw-section-stack">
        <Card>
          <CardHeader>
            <CardTitle>Gateway Operations Status</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="gw-status-grid">
              <div className="gw-status-item">
                <div className="gw-status-item-header">
                  <Server size={16} aria-hidden="true" />
                  <span className="gw-status-item-label">Proxy Pipeline</span>
                </div>
                <StatusPill label="Operational" />
                <span className="gw-status-detail">Transparent SSE & request filtering</span>
              </div>

              <div className="gw-status-item">
                <div className="gw-status-item-header">
                  <Database size={16} aria-hidden="true" />
                  <span className="gw-status-item-label">SQLite History</span>
                </div>
                <StatusPill label="Active" />
                <span className="gw-status-detail">Persistent telemetry & request retention</span>
              </div>

              <div className="gw-status-item">
                <div className="gw-status-item-header">
                  <Activity size={16} aria-hidden="true" />
                  <span className="gw-status-item-label">Upstream Connection</span>
                </div>
                <StatusPill label="Connected" />
                <span className="gw-status-detail">9router upstream protocol bridge</span>
              </div>

              <div className="gw-status-item">
                <div className="gw-status-item-header">
                  <Zap size={16} aria-hidden="true" />
                  <span className="gw-status-item-label">Limiter Engine</span>
                </div>
                <StatusPill label="Enforcing" />
                <span className="gw-status-detail">Request, token & concurrency limits</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </section>
    </div>
  );
};

export default OverviewPage;
