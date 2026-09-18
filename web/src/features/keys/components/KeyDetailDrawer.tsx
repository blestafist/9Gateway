import React from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Drawer,
  StatusPill,
  Badge,
  Button,
  Alert,
  Skeleton,
} from "../../../shared/ui";
import {
  formatTimestamp,
  formatCostMicros,
  formatTokenCount,
  formatDurationSeconds,
} from "../../../shared/formatters";
import { getKeyDetail } from "../api";
import { keyQueryKeys } from "../queryKeys";
import { getKeyStatus } from "../helpers";

export interface KeyDetailDrawerProps {
  keyId: string | null;
  isOpen: boolean;
  onClose: () => void;
}

export const KeyDetailDrawer: React.FC<KeyDetailDrawerProps> = ({
  keyId,
  isOpen,
  onClose,
}) => {
  const {
    data: detail,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: keyQueryKeys.detail(keyId || ""),
    queryFn: ({ signal }) => getKeyDetail(keyId!, signal),
    enabled: Boolean(keyId && isOpen),
    retry: (failureCount, err) => {
      if (err && typeof err === "object" && "status" in err && err.status === 404) {
        return false;
      }
      return failureCount < 2;
    },
  });

  const isNotFound =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    error.status === 404;

  const statusInfo = detail
    ? getKeyStatus(detail.enabled, detail.expires_at)
    : null;

  return (
    <Drawer
      isOpen={isOpen}
      onClose={onClose}
      title={detail ? detail.name : "API Key Details"}
      description={detail ? `Identifier: ${detail.id}` : undefined}
      footer={
        <div style={{ display: "flex", justifyContent: "flex-end", width: "100%" }}>
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
        </div>
      }
    >
      {/* Loading state */}
      {isLoading && (
        <div className="gw-key-detail-content" data-testid="key-detail-loading">
          <div className="gw-key-detail-section">
            <Skeleton height="20px" width="120px" />
            <div className="gw-key-detail-grid">
              <Skeleton height="40px" />
              <Skeleton height="40px" />
            </div>
          </div>
          <div className="gw-key-detail-section">
            <Skeleton height="20px" width="140px" />
            <div className="gw-key-detail-grid">
              <Skeleton height="40px" />
              <Skeleton height="40px" />
            </div>
          </div>
        </div>
      )}

      {/* Deleted / Not Found Error (deleted between pages or invalid link) */}
      {isNotFound && (
        <Alert
          variant="danger"
          title="Key Not Found"
          data-testid="key-not-found-alert"
        >
          The API key with identifier <code>{keyId}</code> could not be found. It may have been deleted or removed.
        </Alert>
      )}

      {/* Other Errors */}
      {isError && !isNotFound && (
        <Alert
          variant="danger"
          title="Failed to Load Key Details"
          action={
            <Button size="sm" variant="secondary" onClick={() => void refetch()}>
              Retry
            </Button>
          }
          data-testid="key-detail-error-alert"
        >
          An unexpected error occurred while fetching details for this API key.
        </Alert>
      )}

      {/* Loaded Content */}
      {!isLoading && !isError && detail && statusInfo && (
        <div className="gw-key-detail-content" data-testid="key-detail-content">
          {/* Section 1: Identity */}
          <section className="gw-key-detail-section" aria-labelledby="detail-identity-title">
            <h3 id="detail-identity-title" className="gw-key-detail-section-title">
              Identity
            </h3>

            <div className="gw-key-detail-grid">
              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Key Name</span>
                <span className="gw-key-detail-value">{detail.name}</span>
              </div>

              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Key ID</span>
                <code className="gw-key-detail-code">{detail.id}</code>
              </div>

              <div className="gw-key-detail-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-detail-label">Display Prefix</span>
                <div>
                  <code className="gw-key-detail-code">{detail.display_prefix}</code>
                  <p className="gw-key-detail-notice">
                    Safe prefix only. Gateway API key secrets are presented only once at creation and cannot be revealed or reconstructed.
                  </p>
                </div>
              </div>
            </div>
          </section>

          {/* Section 2: Status & Expiry */}
          <section className="gw-key-detail-section" aria-labelledby="detail-status-title">
            <h3 id="detail-status-title" className="gw-key-detail-section-title">
              Status & Expiry
            </h3>

            {statusInfo.isExpired && (
              <Alert variant="danger">
                This API key expired on {formatTimestamp(detail.expires_at)}. Requests presenting this token are rejected.
              </Alert>
            )}

            {statusInfo.isExpiringSoon && !statusInfo.isExpired && (
              <Alert variant="warning">
                This API key will expire on {formatTimestamp(detail.expires_at)}. Plan key rotation accordingly.
              </Alert>
            )}

            <div className="gw-key-detail-grid">
              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Current Status</span>
                <div>
                  <StatusPill label={statusInfo.label} variant={statusInfo.variant} />
                </div>
              </div>

              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Expires At</span>
                <span className="gw-key-detail-value">
                  {detail.expires_at ? formatTimestamp(detail.expires_at) : "Never expires"}
                </span>
              </div>

              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Created At</span>
                <span className="gw-key-detail-value">{formatTimestamp(detail.created_at)}</span>
              </div>

              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Updated At</span>
                <span className="gw-key-detail-value">{formatTimestamp(detail.updated_at)}</span>
              </div>
            </div>
          </section>

          {/* Section 3: Policy Summary */}
          <section className="gw-key-detail-section" aria-labelledby="detail-policy-title">
            <h3 id="detail-policy-title" className="gw-key-detail-section-title">
              Policy Summary
            </h3>

            <div className="gw-key-detail-grid">
              {/* Allowed Models */}
              <div className="gw-key-detail-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-detail-label">Allowed Models</span>
                {detail.policy.allowed_models.length > 0 ? (
                  <div className="gw-key-detail-badge-list">
                    {detail.policy.allowed_models.map((model) => (
                      <Badge key={model} variant="neutral" size="sm">
                        {model}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <span className="gw-key-detail-value" style={{ color: "var(--text-secondary)" }}>
                    All models permitted (no allowlist restriction)
                  </span>
                )}
              </div>

              {/* Denied Models */}
              <div className="gw-key-detail-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-detail-label">Denied Models</span>
                {detail.policy.denied_models.length > 0 ? (
                  <div className="gw-key-detail-badge-list">
                    {detail.policy.denied_models.map((model) => (
                      <Badge key={model} variant="warning" size="sm">
                        {model}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <span className="gw-key-detail-value" style={{ color: "var(--text-secondary)" }}>
                    None (no models denied)
                  </span>
                )}
              </div>

              {/* Concurrency Limit */}
              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Max Concurrency</span>
                <span className="gw-key-detail-value">
                  {detail.policy.max_concurrent_requests > 0
                    ? `${detail.policy.max_concurrent_requests} concurrent requests`
                    : "Unlimited"}
                </span>
              </div>

              {/* Token Mode */}
              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Token Counting Mode</span>
                <span className="gw-key-detail-value" style={{ textTransform: "capitalize" }}>
                  {detail.policy.token_mode || "total"}
                </span>
              </div>

              {/* Request Windows */}
              <div className="gw-key-detail-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-detail-label">Request Rate Limits</span>
                {detail.policy.request_windows.length > 0 ? (
                  <ul className="gw-key-detail-list">
                    {detail.policy.request_windows.map((w, i) => (
                      <li key={i}>
                        {w.amount.toLocaleString()} requests per {formatDurationSeconds(w.duration)}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <span className="gw-key-detail-value" style={{ color: "var(--text-secondary)" }}>
                    Unlimited
                  </span>
                )}
              </div>

              {/* Token Windows */}
              <div className="gw-key-detail-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-detail-label">Token Rate Limits</span>
                {detail.policy.token_windows.length > 0 ? (
                  <ul className="gw-key-detail-list">
                    {detail.policy.token_windows.map((w, i) => (
                      <li key={i}>
                        {formatTokenCount(w.amount)} tokens per {formatDurationSeconds(w.duration)} ({detail.policy.token_mode || "total"})
                      </li>
                    ))}
                  </ul>
                ) : (
                  <span className="gw-key-detail-value" style={{ color: "var(--text-secondary)" }}>
                    Unlimited
                  </span>
                )}
              </div>

              {/* Budget Limits */}
              <div className="gw-key-detail-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-detail-label">Budget Limits</span>
                {detail.policy.budget_limits.length > 0 ? (
                  <ul className="gw-key-detail-list">
                    {detail.policy.budget_limits.map((b, i) => (
                      <li key={i}>
                        {formatCostMicros(b.amount_micros)} ({b.period})
                      </li>
                    ))}
                  </ul>
                ) : (
                  <span className="gw-key-detail-value" style={{ color: "var(--text-secondary)" }}>
                    No budget limits configured
                  </span>
                )}
              </div>

              {/* Request & Response Body Logging */}
              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Request Body Logging</span>
                <span className="gw-key-detail-value">
                  {detail.policy.log_request_body ? "Enabled" : "Disabled"}
                </span>
              </div>

              <div className="gw-key-detail-item">
                <span className="gw-key-detail-label">Response Body Logging</span>
                <span className="gw-key-detail-value">
                  {detail.policy.log_response_body ? "Enabled" : "Disabled"}
                </span>
              </div>
            </div>

            <p className="gw-key-detail-notice" style={{ marginTop: "var(--space-2)" }}>
              Policy editing and status toggling will be available in T172.
            </p>
          </section>
        </div>
      )}
    </Drawer>
  );
};
