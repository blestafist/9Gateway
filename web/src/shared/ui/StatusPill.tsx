import React from "react";
import { Badge, BadgeVariant } from "./Badge";

export interface StatusPillProps {
  label: string;
  variant?: BadgeVariant;
}

export const StatusPill: React.FC<StatusPillProps> = ({ label, variant }) => {
  if (variant) {
    return (
      <Badge variant={variant} dot data-testid="status-pill">
        {label}
      </Badge>
    );
  }
  return (
    <span
      data-testid="status-pill"
      style={{
        display: "inline-block",
        padding: "2px 8px",
        borderRadius: "4px",
        border: "1px solid #ccc",
        fontSize: "12px",
        fontFamily: "monospace",
      }}
    >
      {label}
    </span>
  );
};
