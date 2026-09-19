import React from "react";
import { Badge } from "../../../shared/ui";
import { formatTerminalOutcome } from "../../../shared/formatters";
import { getOutcomeVariant } from "../helpers";

export interface RequestOutcomeBadgeProps {
  status: number | null | undefined;
  outcome: string | null | undefined;
}

export const RequestOutcomeBadge: React.FC<RequestOutcomeBadgeProps> = ({
  status,
  outcome,
}) => {
  if (status === null && outcome === null) {
    return <span className="gw-table-dimmed">—</span>;
  }

  const variant = getOutcomeVariant(status, outcome);

  let labelText = "";
  if (status !== null && status !== undefined && outcome) {
    labelText = `${status} ${formatTerminalOutcome(outcome)}`;
  } else if (status !== null && status !== undefined) {
    labelText = String(status);
  } else if (outcome) {
    labelText = formatTerminalOutcome(outcome);
  } else {
    labelText = "—";
  }

  return (
    <Badge
      variant={variant}
      size="sm"
      dot
      data-testid="request-outcome-badge"
      className="gw-outcome-badge"
    >
      {labelText}
    </Badge>
  );
};
