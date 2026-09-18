import React from "react";
import { Link } from "react-router-dom";
import { Card, CardHeader, CardTitle, CardContent, Badge } from "../../../shared/ui";
import { OverviewKeyCounts } from "../types";
import { KeyRound, ArrowRight } from "lucide-react";

export interface KeySummaryCardProps {
  keyCounts: OverviewKeyCounts;
}

export const KeySummaryCard: React.FC<KeySummaryCardProps> = ({ keyCounts }) => {
  const disabledCount = Math.max(0, keyCounts.total - keyCounts.enabled);

  return (
    <Card className="gw-overview-summary-card" data-testid="key-summary-card">
      <CardHeader className="gw-card-header-flex">
        <div className="gw-card-header-title-group">
          <KeyRound size={16} aria-hidden="true" style={{ color: "var(--accent-primary)" }} />
          <CardTitle>API Keys Summary</CardTitle>
        </div>
        <Link to="/ui/keys" className="gw-link-action" aria-label="Manage API keys">
          <span>Manage Keys</span>
          <ArrowRight size={14} aria-hidden="true" />
        </Link>
      </CardHeader>
      <CardContent>
        <div className="gw-key-summary-stats">
          <div className="gw-key-stat-item">
            <span className="gw-key-stat-value">{keyCounts.total}</span>
            <span className="gw-key-stat-label">Total Configured</span>
          </div>
          <div className="gw-key-stat-item">
            <span className="gw-key-stat-value" style={{ color: "var(--status-success)" }}>
              {keyCounts.enabled}
            </span>
            <span className="gw-key-stat-label">Active / Enabled</span>
          </div>
          <div className="gw-key-stat-item">
            <span
              className="gw-key-stat-value"
              style={{ color: disabledCount > 0 ? "var(--status-warning)" : "var(--text-muted)" }}
            >
              {disabledCount}
            </span>
            <span className="gw-key-stat-label">Disabled</span>
          </div>
        </div>

        <div className="gw-key-summary-footer">
          <Badge variant={keyCounts.enabled > 0 ? "success" : "neutral"} size="sm">
            {keyCounts.enabled > 0 ? "Admitting Traffic" : "All Keys Inactive"}
          </Badge>
          <span className="gw-key-summary-subtext">
            Key-level rate, token, and concurrency limits enforced at proxy boundary.
          </span>
        </div>
      </CardContent>
    </Card>
  );
};
