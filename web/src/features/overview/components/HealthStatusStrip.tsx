import React from "react";
import { Link } from "react-router-dom";
import { Card, CardHeader, CardTitle, CardContent, StatusPill, Badge } from "../../../shared/ui";
import { AdminOverviewResponse } from "../types";
import { Server, Activity, ShieldCheck, Database, ArrowRight } from "lucide-react";

export interface HealthStatusStripProps {
  overview: AdminOverviewResponse;
}

export const HealthStatusStrip: React.FC<HealthStatusStripProps> = ({ overview }) => {
  const { current, active_requests, key_counts } = overview;
  const total = current.total_requests;
  const errors = current.error_requests;
  const rejected = current.rejected_requests;

  const isDegraded = total > 0 && (errors + rejected) / total > 0.05;
  const overallHealthLabel = isDegraded ? "Degraded" : "Operational";
  const overallHealthVariant = isDegraded ? "warning" : "success";

  const successRate = total > 0 ? ((current.successful_requests / total) * 100).toFixed(1) + "%" : "No Traffic";

  const busyStatusLabel = active_requests > 0 ? `${active_requests} Active` : "Idle";

  return (
    <section aria-label="Gateway Operational Status" className="gw-section-stack">
      <Card>
        <CardHeader className="gw-status-header">
          <div className="gw-status-header-left">
            <CardTitle>Gateway Health & Operations</CardTitle>
            <Badge variant={overallHealthVariant} size="sm" dot>
              {overallHealthLabel}
            </Badge>
          </div>
          <Link to="/ui/system" className="gw-link-action" aria-label="View system diagnostics">
            <span>System Diagnostics</span>
            <ArrowRight size={14} aria-hidden="true" />
          </Link>
        </CardHeader>
        <CardContent>
          <div className="gw-status-grid">
            <div className="gw-status-item" data-testid="status-pipeline">
              <div className="gw-status-item-header">
                <Server size={16} aria-hidden="true" />
                <span className="gw-status-item-label">Proxy Pipeline</span>
              </div>
              <div>
                <StatusPill label={overallHealthLabel} />
              </div>
              <span className="gw-status-detail">Transparent policy proxying & routing</span>
            </div>

            <div className="gw-status-item" data-testid="status-busy">
              <div className="gw-status-item-header">
                <Activity size={16} aria-hidden="true" />
                <span className="gw-status-item-label">Active Work</span>
              </div>
              <div>
                <StatusPill label={busyStatusLabel} />
              </div>
              <span className="gw-status-detail">
                {active_requests === 0
                  ? "No concurrent in-flight requests"
                  : `${active_requests} request${active_requests === 1 ? "" : "s"} actively streaming/proxying`}
              </span>
            </div>

            <div className="gw-status-item" data-testid="status-success">
              <div className="gw-status-item-header">
                <ShieldCheck size={16} aria-hidden="true" />
                <span className="gw-status-item-label">Success Rate</span>
              </div>
              <div>
                <StatusPill label={successRate} />
              </div>
              <span className="gw-status-detail">
                {total === 0
                  ? "0 total requests in window"
                  : `${current.successful_requests} complete / ${total} total`}
              </span>
            </div>

            <div className="gw-status-item" data-testid="status-storage">
              <div className="gw-status-item-header">
                <Database size={16} aria-hidden="true" />
                <span className="gw-status-item-label">Keys & Storage</span>
              </div>
              <div>
                <StatusPill label={`${key_counts.enabled}/${key_counts.total} Keys`} />
              </div>
              <span className="gw-status-detail">SQLite persistent audit & retention active</span>
            </div>
          </div>
        </CardContent>
      </Card>
    </section>
  );
};
