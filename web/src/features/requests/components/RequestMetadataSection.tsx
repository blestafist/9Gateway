import React, { useState } from "react";
import { Link } from "react-router-dom";
import { Copy, Check } from "lucide-react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Badge,
  IconButton,
} from "../../../shared/ui";
import {
  formatHttpStatus,
  formatTokenCount,
  formatCostMicros,
  formatByteCount,
  formatExactByteCount,
} from "../../../shared/formatters";
import { AdminRequestDetail } from "../types";
import { formatModes } from "../helpers";
import { RequestOutcomeBadge } from "./RequestOutcomeBadge";

export interface RequestMetadataSectionProps {
  detail: AdminRequestDetail;
}

export const RequestMetadataSection: React.FC<RequestMetadataSectionProps> = ({
  detail,
}) => {
  const [copiedId, setCopiedId] = useState(false);

  const handleCopyId = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      void navigator.clipboard.writeText(detail.request_id);
      setCopiedId(true);
      setTimeout(() => setCopiedId(false), 2000);
    }
  };

  return (
    <div
      className="gw-request-metadata-sections"
      data-testid="request-metadata-sections"
    >
      {/* Overview & Identity Card */}
      <Card>
        <CardHeader>
          <CardTitle>Identity & Routing</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="gw-detail-grid">
            <div className="gw-detail-item">
              <dt className="gw-detail-label">Request ID</dt>
              <dd className="gw-detail-value gw-detail-id-row">
                <code
                  className="gw-code-full-id"
                  data-testid="detail-request-id"
                >
                  {detail.request_id}
                </code>
                <IconButton
                  icon={
                    copiedId ? (
                      <Check size={14} aria-hidden="true" />
                    ) : (
                      <Copy size={14} aria-hidden="true" />
                    )
                  }
                  aria-label={
                    copiedId
                      ? `Copied request ID ${detail.request_id}`
                      : `Copy request ID ${detail.request_id}`
                  }
                  variant="ghost"
                  size="sm"
                  onClick={handleCopyId}
                  data-testid="detail-copy-id-btn"
                />
                <span className="sr-only" aria-live="polite">
                  {copiedId ? "Request ID copied to clipboard" : ""}
                </span>
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">API Key</dt>
              <dd className="gw-detail-value" data-testid="detail-api-key">
                {detail.api_key_id ? (
                  <Link
                    to={`/keys/${encodeURIComponent(detail.api_key_id)}`}
                    className="gw-detail-key-link"
                    title={detail.api_key_id}
                  >
                    {detail.api_key_name || detail.api_key_id}
                    <code className="gw-key-prefix-badge">
                      {detail.api_key_id}
                    </code>
                  </Link>
                ) : (
                  <span className="gw-table-dimmed">None (No key bound)</span>
                )}
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Method & Route</dt>
              <dd className="gw-detail-value" data-testid="detail-route">
                <span className="gw-method-badge">{detail.method || "—"}</span>{" "}
                <code className="gw-route-path">{detail.path || detail.route || "—"}</code>
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Model</dt>
              <dd className="gw-detail-value" data-testid="detail-model">
                {detail.model ? (
                  <code className="gw-model-code">{detail.model}</code>
                ) : (
                  <span className="gw-table-dimmed">—</span>
                )}
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>

      {/* Status, Outcome & Modes Card */}
      <Card>
        <CardHeader>
          <CardTitle>Status, Outcome & Modes</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="gw-detail-grid">
            <div className="gw-detail-item">
              <dt className="gw-detail-label">Downstream Status</dt>
              <dd className="gw-detail-value" data-testid="detail-downstream-status">
                {detail.downstream_status !== null && detail.downstream_status !== undefined ? (
                  <span className="gw-status-code-val">
                    {formatHttpStatus(detail.downstream_status)}
                  </span>
                ) : (
                  <span className="gw-table-dimmed">—</span>
                )}
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Upstream Status</dt>
              <dd className="gw-detail-value" data-testid="detail-upstream-status">
                {detail.upstream_status !== null && detail.upstream_status !== undefined ? (
                  <span className="gw-status-code-val">
                    {formatHttpStatus(detail.upstream_status)}
                  </span>
                ) : (
                  <span className="gw-table-dimmed">—</span>
                )}
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Terminal Outcome</dt>
              <dd className="gw-detail-value" data-testid="detail-outcome">
                <RequestOutcomeBadge
                  status={detail.downstream_status}
                  outcome={detail.terminal_outcome}
                />
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Upstream Started</dt>
              <dd className="gw-detail-value" data-testid="detail-upstream-started">
                <Badge
                  variant={detail.upstream_started ? "success" : "neutral"}
                  size="sm"
                >
                  {detail.upstream_started ? "Started" : "Not Started"}
                </Badge>
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Streaming Modes</dt>
              <dd className="gw-detail-value" data-testid="detail-modes">
                <span className="gw-modes-text">
                  {formatModes(
                    detail.requested_mode,
                    detail.upstream_mode,
                    detail.delivered_mode
                  )}
                </span>
                {detail.requested_mode && detail.delivered_mode && (
                  <div className="gw-modes-breakdown gw-table-dimmed">
                    <span>req: {detail.requested_mode}</span>
                    {detail.upstream_mode && <span> | up: {detail.upstream_mode}</span>}
                    <span> | del: {detail.delivered_mode}</span>
                  </div>
                )}
              </dd>
            </div>

            <div className="gw-detail-item">
              <dt className="gw-detail-label">Error Code</dt>
              <dd className="gw-detail-value" data-testid="detail-error-code">
                {detail.error_code ? (
                  <Badge variant="danger" size="sm">
                    {detail.error_code}
                  </Badge>
                ) : (
                  <span className="gw-table-dimmed">—</span>
                )}
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>

      {/* Accounting & Usage Card (Tokens, Cost, Bytes) */}
      <Card>
        <CardHeader>
          <CardTitle>Usage & Accounting</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="gw-accounting-columns">
            {/* Tokens Column */}
            <div className="gw-accounting-column">
              <h4 className="gw-column-subheading">Tokens</h4>
              <dl className="gw-accounting-list">
                <div className="gw-accounting-row">
                  <dt>Input Tokens</dt>
                  <dd data-testid="detail-input-tokens">
                    {formatTokenCount(detail.input_tokens)}
                  </dd>
                </div>
                <div className="gw-accounting-row">
                  <dt>Cached Input</dt>
                  <dd data-testid="detail-cached-tokens">
                    {formatTokenCount(detail.cached_input_tokens)}
                  </dd>
                </div>
                <div className="gw-accounting-row">
                  <dt>Output Tokens</dt>
                  <dd data-testid="detail-output-tokens">
                    {formatTokenCount(detail.output_tokens)}
                  </dd>
                </div>
                <div className="gw-accounting-row">
                  <dt>Reasoning Output</dt>
                  <dd data-testid="detail-reasoning-tokens">
                    {formatTokenCount(detail.reasoning_output_tokens)}
                  </dd>
                </div>
                <div className="gw-accounting-row gw-accounting-total">
                  <dt>Total Tokens</dt>
                  <dd data-testid="detail-total-tokens">
                    {formatTokenCount(detail.total_tokens)}
                  </dd>
                </div>
              </dl>
            </div>

            {/* Cost Column */}
            <div className="gw-accounting-column">
              <h4 className="gw-column-subheading">Cost</h4>
              <dl className="gw-accounting-list">
                <div className="gw-accounting-row gw-accounting-total">
                  <dt>Estimated Cost</dt>
                  <dd data-testid="detail-cost">
                    {formatCostMicros(detail.cost_micros)}
                  </dd>
                </div>
                {detail.cost_micros !== null && detail.cost_micros !== undefined && (
                  <div className="gw-accounting-row">
                    <dt>Micro-dollars</dt>
                    <dd className="gw-table-dimmed">
                      {detail.cost_micros.toLocaleString()} µ$
                    </dd>
                  </div>
                )}
              </dl>
            </div>

            {/* Bytes Column */}
            <div className="gw-accounting-column">
              <h4 className="gw-column-subheading">Payload Bytes</h4>
              <dl className="gw-accounting-list">
                <div className="gw-accounting-row">
                  <dt>Client Bytes</dt>
                  <dd data-testid="detail-client-bytes">
                    {detail.client_bytes !== null && detail.client_bytes !== undefined ? (
                      <span title={formatExactByteCount(detail.client_bytes)}>
                        {formatByteCount(detail.client_bytes)}
                      </span>
                    ) : (
                      "—"
                    )}
                  </dd>
                </div>
                <div className="gw-accounting-row">
                  <dt>Upstream Bytes</dt>
                  <dd data-testid="detail-upstream-bytes">
                    {detail.upstream_bytes !== null && detail.upstream_bytes !== undefined ? (
                      <span title={formatExactByteCount(detail.upstream_bytes)}>
                        {formatByteCount(detail.upstream_bytes)}
                      </span>
                    ) : (
                      "—"
                    )}
                  </dd>
                </div>
                <div className="gw-accounting-row">
                  <dt>Delivered Bytes</dt>
                  <dd data-testid="detail-delivered-bytes">
                    {detail.delivered_bytes !== null && detail.delivered_bytes !== undefined ? (
                      <span title={formatExactByteCount(detail.delivered_bytes)}>
                        {formatByteCount(detail.delivered_bytes)}
                      </span>
                    ) : (
                      "—"
                    )}
                  </dd>
                </div>
              </dl>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
};
