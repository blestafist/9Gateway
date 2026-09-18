import React from "react";
import { Link } from "react-router-dom";
import { Card, CardHeader, CardTitle, CardContent } from "../../../shared/ui";
import { BarChart3, ArrowRight } from "lucide-react";

export const UsageLinkCard: React.FC = () => {
  return (
    <Card className="gw-overview-summary-card" data-testid="usage-link-card">
      <CardHeader className="gw-card-header-flex">
        <div className="gw-card-header-title-group">
          <BarChart3 size={16} aria-hidden="true" style={{ color: "var(--accent-primary)" }} />
          <CardTitle>Usage & Analytics</CardTitle>
        </div>
        <Link to="/ui/usage" className="gw-link-action" aria-label="View usage analytics">
          <span>Explore Usage</span>
          <ArrowRight size={14} aria-hidden="true" />
        </Link>
      </CardHeader>
      <CardContent>
        <p className="gw-card-description">
          Analyze historical request volumes, token usage breakdowns, latency observations, and provider cost
          aggregations across all retained gateway records.
        </p>
        <div className="gw-link-card-action-row">
          <Link to="/ui/usage" className="gw-btn gw-btn--secondary gw-btn--sm">
            View Analytics Dashboard
          </Link>
        </div>
      </CardContent>
    </Card>
  );
};
