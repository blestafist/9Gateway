import React, { useState } from "react";
import { Link } from "react-router-dom";
import { Copy, Check } from "lucide-react";
import { IconButton } from "../../../shared/ui";
import {
  formatTimestamp,
  formatDurationMicros,
  formatCostMicros,
  formatTokenCount,
} from "../../../shared/formatters";
import { AdminRequestListItem } from "../types";
import { truncateId, formatModes } from "../helpers";
import { RequestOutcomeBadge } from "./RequestOutcomeBadge";

export interface RequestCardListProps {
  requests: AdminRequestListItem[];
  onSelectRequest: (requestId: string) => void;
}

export const RequestCardList: React.FC<RequestCardListProps> = ({
  requests,
  onSelectRequest,
}) => {
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
    <div className="gw-requests-cards-view" data-testid="requests-cards-view">
      {requests.map((req) => {
        const isCopied = copiedId === req.request_id;
        const modesText = formatModes(
          req.requested_mode,
          req.upstream_mode,
          req.delivered_mode
        );

        return (
          <div
            key={req.request_id}
            className="gw-request-mobile-card"
            onClick={() => onSelectRequest(req.request_id)}
            tabIndex={0}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onSelectRequest(req.request_id);
              }
            }}
            data-testid={`request-card-${req.request_id}`}
            role="button"
            aria-label={`Request ${req.request_id}`}
          >
            {/* Top row: Method + Route + Outcome badge */}
            <div className="gw-request-card-header">
              <div className="gw-request-card-route">
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
              <RequestOutcomeBadge
                status={req.downstream_status}
                outcome={req.terminal_outcome}
              />
            </div>

            {/* Request ID + copy + deep link */}
            <div className="gw-request-card-id-row">
              <span className="gw-card-meta-label">ID:</span>
              <Link
                to={`/requests/${encodeURIComponent(req.request_id)}`}
                className="gw-request-id-link"
                onClick={(e) => e.stopPropagation()}
                title={req.request_id}
              >
                <code className="gw-code-id">{truncateId(req.request_id, 10, 8)}</code>
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
                data-testid={`card-copy-id-${req.request_id}`}
              />
            </div>

            {/* Grid of metadata details */}
            <div className="gw-request-card-meta-grid">
              <div className="gw-request-card-meta-item">
                <span className="gw-card-meta-label">Model</span>
                <span className="gw-card-meta-value">
                  {req.model ? (
                    <span className="gw-model-badge">{req.model}</span>
                  ) : (
                    "—"
                  )}
                </span>
              </div>

              <div className="gw-request-card-meta-item">
                <span className="gw-card-meta-label">Key</span>
                <span className="gw-card-meta-value">
                  {req.api_key_id ? (
                    <Link
                      to={`/keys/${encodeURIComponent(req.api_key_id)}`}
                      className="gw-request-key-link"
                      onClick={(e) => e.stopPropagation()}
                    >
                      {req.api_key_name || req.api_key_id}
                    </Link>
                  ) : (
                    "—"
                  )}
                </span>
              </div>

              <div className="gw-request-card-meta-item">
                <span className="gw-card-meta-label">Tokens</span>
                <span className="gw-card-meta-value gw-num-tabular">
                  {formatTokenCount(req.total_tokens)}
                </span>
              </div>

              <div className="gw-request-card-meta-item">
                <span className="gw-card-meta-label">Est. Cost</span>
                <span className="gw-card-meta-value gw-num-tabular">
                  {formatCostMicros(req.cost_micros)}
                </span>
              </div>

              <div className="gw-request-card-meta-item">
                <span className="gw-card-meta-label">Duration</span>
                <span className="gw-card-meta-value gw-num-tabular">
                  {formatDurationMicros(req.total_micros)}
                </span>
              </div>

              <div className="gw-request-card-meta-item">
                <span className="gw-card-meta-label">Modes</span>
                <span className="gw-card-meta-value gw-requests-modes">
                  {modesText}
                </span>
              </div>
            </div>

            {/* Bottom: Completion Time */}
            <div className="gw-request-card-footer">
              <span className="gw-card-meta-label">Completed:</span>
              <span
                className="gw-table-dimmed gw-requests-time"
                title={req.finished_at ?? undefined}
              >
                {formatTimestamp(req.finished_at)}
              </span>
            </div>
          </div>
        );
      })}

      {/* Screen reader notification for copy ID */}
      <div className="gw-sr-only" aria-live="polite" aria-atomic="true">
        {copiedId ? `Request ID ${copiedId} copied to clipboard.` : ""}
      </div>
    </div>
  );
};
