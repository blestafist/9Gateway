import React from "react";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  StatusPill,
  Badge,
  Button,
} from "../../../shared/ui";
import { formatTimestamp } from "../../../shared/formatters";
import { AdminKeyListItem } from "../types";
import { formatKeyExpiry, getKeyStatus } from "../helpers";

export interface KeyTableProps {
  keys: AdminKeyListItem[];
  onSelectKey: (id: string) => void;
}

export const KeyTable: React.FC<KeyTableProps> = ({ keys, onSelectKey }) => {
  return (
    <div className="gw-keys-table-view" data-testid="keys-desktop-table">
      <Table aria-label="API Keys">
        <TableHeader>
          <TableRow>
            <TableHead>Name & ID</TableHead>
            <TableHead>Prefix</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Expires</TableHead>
            <TableHead>Policy</TableHead>
            <TableHead style={{ textAlign: "right" }}>Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {keys.map((key) => {
            const statusInfo = getKeyStatus(key.enabled, key.expires_at);

            return (
              <TableRow
                key={key.id}
                className="gw-keys-table-row"
                tabIndex={0}
                role="button"
                aria-label={`View details for ${key.name}`}
                onClick={() => onSelectKey(key.id)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    onSelectKey(key.id);
                  }
                }}
                data-testid={`key-row-${key.id}`}
              >
                {/* Name & ID */}
                <TableCell>
                  <div className="gw-key-name-col">
                    <span className="gw-key-name" title={key.name}>
                      {key.name}
                    </span>
                    <span className="gw-key-id-sub" title={key.id}>
                      {key.id}
                    </span>
                  </div>
                </TableCell>

                {/* Safe Prefix */}
                <TableCell>
                  <code className="gw-key-prefix">{key.display_prefix}</code>
                </TableCell>

                {/* Status (Not color-only) */}
                <TableCell>
                  <StatusPill label={statusInfo.label} variant={statusInfo.variant} />
                </TableCell>

                {/* Created */}
                <TableCell style={{ fontSize: "var(--font-size-xs)", whiteSpace: "nowrap" }}>
                  {formatTimestamp(key.created_at)}
                </TableCell>

                {/* Expiry */}
                <TableCell style={{ fontSize: "var(--font-size-xs)", whiteSpace: "nowrap" }}>
                  {formatKeyExpiry(key.expires_at)}
                </TableCell>

                {/* Policy Summary */}
                <TableCell>
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
                          Standard
                        </span>
                      )}
                  </div>
                </TableCell>

                {/* Actions */}
                <TableCell style={{ textAlign: "right" }}>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      onSelectKey(key.id);
                    }}
                    aria-label={`View details for ${key.name}`}
                  >
                    Details
                  </Button>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
};
