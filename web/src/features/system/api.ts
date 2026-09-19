import { adminFetch } from "../../shared/transport";
import { SystemResponse, ReadinessResponse } from "./types";
import { validateSystemResponse, validateReadinessResponse } from "./validation";

export async function getSystem(signal?: AbortSignal): Promise<SystemResponse> {
  return adminFetch<SystemResponse>("/admin/v1/system", {
    method: "GET",
    signal,
    allowedStatuses: [200],
    validate: validateSystemResponse,
  });
}

// Retained for backward-compatibility if needed
export async function getReadiness(signal?: AbortSignal): Promise<ReadinessResponse> {
  return adminFetch<ReadinessResponse>("/ready", {
    method: "GET",
    signal,
    allowedStatuses: [200, 503],
    validate: validateReadinessResponse,
  });
}
