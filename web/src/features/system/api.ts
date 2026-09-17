import { adminFetch } from "../../shared/transport";
import { ReadinessResponse } from "./types";
import { validateReadinessResponse } from "./validation";

export async function getReadiness(signal?: AbortSignal): Promise<ReadinessResponse> {
  return adminFetch<ReadinessResponse>("/ready", {
    method: "GET",
    signal,
    allowedStatuses: [200, 503],
    validate: validateReadinessResponse,
  });
}
