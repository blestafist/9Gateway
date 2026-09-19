import React, { useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { Copy, Check } from "lucide-react";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  IconButton,
} from "../../../shared/ui";
import {
  formatTimestamp,
  formatDurationMicros,
  formatCostMicros,
  formatTokenCount,
} from "../../../shared/formatters";
import { AdminRequestListItem } from "../types";
import { truncateId, formatModes } from "../helpers";
import { RequestOutcomeBadge } from "./RequestOutcomeBadge";

export interface RequestTableProps {
  requests: AdminRequestListItem[];
  onSelectRequest: (requestId: string) => void;
}

export const RequestTable: React.FC<RequestTableProps> = ({
  requests,
  onSelectRequest,
}) => {
  const location = useLocation();
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const handleCopy = (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      void navigator.clipboard.writeText(id);
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 2000);
    }
  };

  return (
    <div className="gw-requests-table-view" data-testid="requests-table-view">
      <div className="gw-table-container">
        <Table aria-label="Recent Requests">
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Request ID</TableHead>
              <TableHead>Key</TableHead>
              <TableHead>Route</TableHead>
              <TableHead>Model</TableHead>
              <TableHead>Status & Outcome</TableHead>
              <TableHead>Modes</TableHead>
              <TableHead align="right">Tokens</TableHead>
              <TableHead align="right">Est. Cost</TableHead>
              <TableHead align="right">Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {requests.map((req) => {
              const isCopied = copiedId === req.request_id;
              const modesText = formatModes(
                req.requested_mode,
                req.upstream_mode,
                req.delivered_mode
              );

              return (
                <TableRow
                  key={req.request_id}
                  className="gw-requests-table-row"
                  onClick={() => onSelectRequest(req.request_id)}
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelectRequest(req.request_id);
                    }
                  }}
                  data-testid={`request-row-${req.request_id}`}
                >
                  {/* Completion Time */}
                  <TableCell>
                    <span
                      className="gw-table-dimmed gw-requests-time"
                      title={req.finished_at ?? undefined}
                    >
                      {formatTimestamp(req.finished_at)}
                    </span>
                  </TableCell>

                  {/* Request ID + Deep Link + Copy Action */}
                  <TableCell>
                    <div className="gw-request-id-cell">
                      <Link
                        to={{
                          pathname: `/requests/${encodeURIComponent(req.request_id)}`,
                          search: location.search,
                        }}
                        className="gw-request-id-link"
                        onClick={(e) => e.stopPropagation()}
                        title={req.request_id}
                      >
                        <code className="gw-code-id">
                          {truncateId(req.request_id)}
                        </code>
                      </Link>
                      <IconButton
                        icon={
                          isCopied ? (
                            <Check size={13} aria-hidden="true" />
                          ) : (
                            <Copy size={13} aria-hidden="true" />
                          )
                        }
                        aria-label={
                          isCopied
                            ? `Copied request ID ${req.request_id}`
                            : `Copy request ID ${req.request_id}`
                        }
                        variant="ghost"
                        size="sm"
                        className="gw-copy-id-btn"
                        onClick={(e) => handleCopy(req.request_id, e)}
                        data-testid={`copy-id-${req.request_id}`}
                      />
                    </div>
                  </TableCell>

                  {/* API Key */}
                  <TableCell>
                    {req.api_key_id ? (
                      <Link
                        to={`/keys/${encodeURIComponent(req.api_key_id)}`}
                        className="gw-request-key-link"
                        onClick={(e) => e.stopPropagation()}
                        title={req.api_key_id}
                      >
                        <span className="gw-request-key-name">
                          {req.api_key_name || req.api_key_id}
                        </span>
                      </Link>
                    ) : (
                      <span className="gw-table-dimmed">—</span>
                    )}
                  </TableCell>

                  {/* Method + Route */}
                  <TableCell>
                    <div className="gw-request-route-box">
                      {req.method && (
                        <span className="gw-method-badge">{req.method}</span>
                      )}
                      <span
                        className="gw-request-path"
                        title={req.route || req.path || undefined}
                      >
                        {req.route || req.path || "—"}
                      </span>
                    </div>
                  </TableCell>

                  {/* Model */}
                  <TableCell>
                    {req.model ? (
                      <span className="gw-model-badge" title={req.model}>
                        {req.model}
                      </span>
                    ) : (
                      <span className="gw-table-dimmed">—</span>
                    )}
                  </TableCell>

                  {/* Status & Outcome */}
                  <TableCell>
                    <RequestOutcomeBadge
                      status={req.downstream_status}
                      outcome={req.terminal_outcome}
                    />
                  </TableCell>

                  {/* Modes */}
                  <TableCell>
                    <span className="gw-requests-modes" title={modesText}>
                      {modesText}
                    </span>
                  </TableCell>

                  {/* Tokens */}
                  <TableCell align="right">
                    <span className="gw-num-tabular">
                      {formatTokenCount(req.total_tokens)}
                    </span>
                  </TableCell>

                  {/* Estimated Cost */}
                  <TableCell align="right">
                    <span className="gw-num-tabular">
                      {formatCostMicros(req.cost_micros)}
                    </span>
                  </TableCell>

                  {/* Duration */}
                  <TableCell align="right">
                    <span className="gw-num-tabular">
                      {formatDurationMicros(req.total_micros)}
                    </span>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      {/* Screen reader notification for copy ID */}
      <div className="gw-sr-only" aria-live="polite" aria-atomic="true">
        {copiedId ? `Request ID ${copiedId} copied to clipboard.` : ""}
      </div>
    </div>
  );
};
