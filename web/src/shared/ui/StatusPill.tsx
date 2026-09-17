import React from "react";

export interface StatusPillProps {
  label: string;
}

export const StatusPill: React.FC<StatusPillProps> = ({ label }) => {
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
