import React from "react";
import { Alert } from "../../../shared/ui";
import { formatTimestamp } from "../../../shared/formatters";
import { Clock, Info } from "lucide-react";
import { UsageRangePreset } from "../types";

export interface RetentionNoticeProps {
  preset: UsageRangePreset;
  retentionLimited: boolean;
  earliestRetainedAt: string | null;
  latestRetainedAt: string | null;
}

export const RetentionNotice: React.FC<RetentionNoticeProps> = ({
  preset,
  retentionLimited,
  earliestRetainedAt,
  latestRetainedAt,
}) => {
  if (retentionLimited) {
    return (
      <div className="gw-retention-notice-wrapper" data-testid="retention-limited-notice">
        <Alert
          variant="warning"
          title="Retention Boundary Reached"
          icon={<Clock size={16} />}
        >
          Telemetry in this range has reached the gateway's history retention limit.
          {earliestRetainedAt ? (
            <> Earliest available observation is <strong>{formatTimestamp(earliestRetainedAt)}</strong>.</>
          ) : null}
          {" "}Prior request records have been pruned according to retention policy.
        </Alert>
      </div>
    );
  }

  if (preset === "all") {
    return (
      <div className="gw-retention-notice-wrapper" data-testid="all-retained-notice">
        <Alert
          variant="info"
          title="All Retained History"
          icon={<Info size={16} />}
        >
          Displaying all request records currently stored under the configured history retention policy
          {earliestRetainedAt && latestRetainedAt ? (
            <> (from <strong>{formatTimestamp(earliestRetainedAt)}</strong> to <strong>{formatTimestamp(latestRetainedAt)}</strong>)</>
          ) : earliestRetainedAt ? (
            <> (from <strong>{formatTimestamp(earliestRetainedAt)}</strong> to present)</>
          ) : null}
          . This reflects current SQLite database contents, not the cumulative lifetime of the gateway.
        </Alert>
      </div>
    );
  }

  return null;
};
