import React from "react";
import { Badge, BadgeVariant } from "../../../shared/ui";
import { UsageDeltaResult } from "../deltas";
import { TrendingUp, TrendingDown, Minus } from "lucide-react";

export interface DeltaBadgeProps {
  delta: UsageDeltaResult;
  className?: string;
}

export const DeltaBadge: React.FC<DeltaBadgeProps> = ({ delta, className = "" }) => {
  let badgeVariant: BadgeVariant = "neutral";
  if (delta.sentiment === "warning") {
    badgeVariant = "danger";
  } else if (delta.sentiment === "positive") {
    badgeVariant = "success";
  }

  const renderIcon = () => {
    if (
      delta.isUnavailable ||
      delta.direction === "unchanged" ||
      delta.direction === "unavailable"
    ) {
      return <Minus size={12} aria-hidden="true" />;
    }
    if (delta.direction === "increase") {
      return <TrendingUp size={12} aria-hidden="true" />;
    }
    return <TrendingDown size={12} aria-hidden="true" />;
  };

  const displayText = delta.formattedPercentage ?? "—";

  return (
    <div className={`gw-delta-row ${className}`} title={delta.comparisonText}>
      <Badge
        variant={badgeVariant}
        size="sm"
        className="gw-delta-badge"
        aria-label={delta.comparisonText}
      >
        <span className="gw-delta-icon">{renderIcon()}</span>
        <span>{displayText}</span>
      </Badge>
      <span className="gw-delta-comparison" aria-hidden="true">
        {delta.comparisonText}
      </span>
    </div>
  );
};
