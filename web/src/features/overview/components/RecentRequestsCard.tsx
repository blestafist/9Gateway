import React from "react";
import { Link } from "react-router-dom";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableCell,
  TableHead,
  Badge,
  EmptyState,
} from "../../../shared/ui";
import { OverviewRecentRequest } from "../types";
import {
  formatDurationMicros,
  formatCostMicros,
  formatTokenCount,
  formatTimestamp,
  formatTerminalOutcome,
} from "../../../shared/formatters";
import { ListFilter, ArrowRight, Inbox } from "lucide-react";

export interface RecentRequestsCardProps {
  requests: OverviewRecentRequest[];
}

export const RecentRequestsCard: React.FC<RecentRequestsCardProps> = ({ requests }) => {
  const renderStatusBadge = (req: OverviewRecentRequest) => {
    const isSuccess = req.terminal_outcome === "complete" || req.terminal_outcome === "custom_dispatch";
    const isRejected = req.terminal_outcome === "pre_upstream";
    const statusText = req.downstream_status ? String(req.downstream_status) : "—";

    let variant: "success" | "danger" | "warning" | "neutral" = "neutral";
    if (isSuccess) {
      variant = "success";
    } else if (isRejected) {
      variant = "warning";
    } else if (req.downstream_status && req.downstream_status >= 400) {
      variant = "danger";
    }

    return (
      <div className="gw-recent-status-group">
        <Badge variant={variant} size="sm">
          {statusText}
        </Badge>
        <span className="gw-recent-outcome-label">
          {formatTerminalOutcome(req.terminal_outcome || req.error_code)}
        </span>
      </div>
    );
  };

  return (
    <Card className="gw-recent-requests-card" data-testid="recent-requests-card">
      <CardHeader className="gw-card-header-flex">
        <div className="gw-card-header-title-group">
          <ListFilter size={16} aria-hidden="true" style={{ color: "var(--accent-primary)" }} />
          <CardTitle>Recent Requests</CardTitle>
        </div>
        <Link to="/ui/requests" className="gw-link-action" aria-label="View all requests in request log">
          <span>View All Requests</span>
          <ArrowRight size={14} aria-hidden="true" />
        </Link>
      </CardHeader>
      <CardContent className="gw-recent-requests-body">
        {requests.length === 0 ? (
          <EmptyState
            icon={<Inbox size={32} />}
            title="No Recent Requests"
            description="No requests were recorded during this period. As traffic is proxied, real-time records will appear here."
          />
        ) : (
          <Table className="gw-recent-table">
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>Status & Outcome</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Key</TableHead>
                <TableHead className="gw-align-right">Duration</TableHead>
                <TableHead className="gw-align-right">Tokens</TableHead>
                <TableHead className="gw-align-right">Est. Cost</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {requests.map((req) => (
                <TableRow key={req.request_id} data-testid={`recent-request-row-${req.request_id}`}>
                  <TableCell className="gw-nowrap-cell gw-font-mono">
                    <span title={req.started_at || undefined}>
                      {formatTimestamp(req.started_at)}
                    </span>
                  </TableCell>
                  <TableCell>{renderStatusBadge(req)}</TableCell>
                  <TableCell className="gw-model-cell">
                    <span className="gw-truncate-cell" title={req.model || "—"}>
                      {req.model || "—"}
                    </span>
                  </TableCell>
                  <TableCell className="gw-key-cell">
                    <span className="gw-truncate-cell" title={req.api_key_name || req.api_key_id || "—"}>
                      {req.api_key_name || req.api_key_id || "—"}
                    </span>
                  </TableCell>
                  <TableCell className="gw-align-right gw-font-mono">
                    {formatDurationMicros(req.total_micros)}
                  </TableCell>
                  <TableCell className="gw-align-right gw-font-mono">
                    {formatTokenCount(req.total_tokens)}
                  </TableCell>
                  <TableCell className="gw-align-right gw-font-mono gw-cost-cell">
                    {formatCostMicros(req.cost_micros)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
};
