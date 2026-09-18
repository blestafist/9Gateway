import React from "react";
import { Card, CardContent, Badge, BadgeVariant } from "../../../shared/ui";
import { DeltaResult } from "../deltas";
import { DeltaBadge } from "./DeltaBadge";

export interface MetricCardProps {
  title: string;
  value: React.ReactNode;
  valueColorVar?: string;
  badgeText?: string;
  badgeVariant?: BadgeVariant;
  subtext?: React.ReactNode;
  delta?: DeltaResult;
  testId?: string;
  className?: string;
}

export const MetricCard: React.FC<MetricCardProps> = ({
  title,
  value,
  valueColorVar = "var(--text-primary)",
  badgeText,
  badgeVariant = "neutral",
  subtext,
  delta,
  testId,
  className = "",
}) => {
  return (
    <Card className={`gw-kpi-card ${className}`} data-testid={testId}>
      <CardContent>
        <div className="gw-kpi-label-row">
          <span className="gw-kpi-label">{title}</span>
          {badgeText && (
            <Badge variant={badgeVariant} size="sm">
              {badgeText}
            </Badge>
          )}
        </div>

        <div className="gw-kpi-value" style={{ color: valueColorVar }}>
          {value}
        </div>

        {delta && <DeltaBadge delta={delta} />}

        {subtext && <span className="gw-kpi-subtext">{subtext}</span>}
      </CardContent>
    </Card>
  );
};
