import { adminFetch } from "../../shared/transport";
import { AdminOverviewResponse, OverviewQueryParams } from "./types";
import { validateOverviewResponse } from "./validation";

export async function getOverview(
  params: OverviewQueryParams = {},
  signal?: AbortSignal
): Promise<AdminOverviewResponse> {
  const query = new URLSearchParams();
  if (params.after) {
    query.set("after", params.after);
  }
  if (params.before) {
    query.set("before", params.before);
  }

  const qs = query.toString();
  const url = qs ? `/admin/v1/overview?${qs}` : "/admin/v1/overview";

  return adminFetch<AdminOverviewResponse>(url, {
    method: "GET",
    signal,
    validate: validateOverviewResponse,
  });
}
