import React from "react";
import { StatusPill } from "../../../shared";

export interface SmokeStatusProps {
  status?: string;
}

export const SmokeStatus: React.FC<SmokeStatusProps> = ({
  status = "ready",
}) => {
  return (
    <div data-testid="smoke-status">
      <span>Gateway status: </span>
      <StatusPill label={status} />
    </div>
  );
};
