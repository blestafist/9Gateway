import React from "react";
import { StatusPill, Badge, Button } from "../../../shared/ui";
import { formatTimestamp } from "../../../shared/formatters";
import { AdminKeyListItem } from "../types";
import { formatKeyExpiry, getKeyStatus } from "../helpers";

export interface KeyCardListProps {
  keys: AdminKeyListItem[];
  onSelectKey: (id: string) => void;
}

export const KeyCardList: React.FC<KeyCardListProps> = ({ keys, onSelectKey }) => {
  return (
    <div className="gw-keys-cards-view" role="list" aria-label="API Keys List">
      {keys.map((key) => {
        const statusInfo = getKeyStatus(key.enabled, key.expires_at);

        return (
          <div
            key={key.id}
            className="gw-key-mobile-card"
            role="listitem"
            tabIndex={0}
            onClick={() => onSelectKey(key.id)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onSelectKey(key.id);
              }
            }}
            data-testid={`key-card-${key.id}`}
            aria-label={`Key ${key.name}`}
          >
            {/* Header */}
            <div className="gw-key-mobile-header">
              <div className="gw-key-mobile-title-box">
                <span className="gw-key-name" title={key.name}>
                  {key.name}
                </span>
                <span className="gw-key-id-sub" title={key.id}>
                  {key.id}
                </span>
              </div>
              <StatusPill label={statusInfo.label} variant={statusInfo.variant} />
            </div>

            {/* Metadata Grid */}
            <div className="gw-key-mobile-meta-grid">
              <div className="gw-key-mobile-meta-item">
                <span className="gw-key-mobile-meta-label">Prefix</span>
                <code className="gw-key-prefix">{key.display_prefix}</code>
              </div>

              <div className="gw-key-mobile-meta-item">
                <span className="gw-key-mobile-meta-label">Expires</span>
                <span style={{ color: "var(--text-primary)" }}>
                  {formatKeyExpiry(key.expires_at)}
                </span>
              </div>

              <div className="gw-key-mobile-meta-item" style={{ gridColumn: "1 / -1" }}>
                <span className="gw-key-mobile-meta-label">Created</span>
                <span style={{ color: "var(--text-secondary)" }}>
                  {formatTimestamp(key.created_at)}
                </span>
              </div>
            </div>

            {/* Policy Tags */}
            <div className="gw-key-policy-tags">
              {key.policy_summary.allow_models && (
                <Badge variant="neutral" size="sm">Allowlist</Badge>
              )}
              {key.policy_summary.deny_models && (
                <Badge variant="warning" size="sm">Denylist</Badge>
              )}
              {key.policy_summary.log_request_body && (
                <Badge variant="neutral" size="sm">Log Req</Badge>
              )}
              {key.policy_summary.log_response_body && (
                <Badge variant="neutral" size="sm">Log Res</Badge>
              )}
              {!key.policy_summary.allow_models &&
                !key.policy_summary.deny_models &&
                !key.policy_summary.log_request_body &&
                !key.policy_summary.log_response_body && (
                  <span style={{ fontSize: "var(--font-size-xs)", color: "var(--text-muted)" }}>
                    Standard Policy
                  </span>
                )}
            </div>

            {/* Card Footer / Action */}
            <div className="gw-key-mobile-actions">
              <Button
                variant="secondary"
                size="sm"
                onClick={(e) => {
                  e.stopPropagation();
                  onSelectKey(key.id);
                }}
                aria-label={`View details for ${key.name}`}
              >
                View Details
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
};
