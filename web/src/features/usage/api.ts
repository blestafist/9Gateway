import { adminFetch } from "../../shared/transport";
import {
  UsageTimeseriesParams,
  UsageTimeseriesResponse,
  UsageBreakdownParams,
  UsageBreakdownResponse,
} from "./types";
import {
  validateUsageTimeseriesResponse,
  validateUsageBreakdownResponse,
} from "./validation";

export async function getUsageTimeseries(
  params: UsageTimeseriesParams = {},
  signal?: AbortSignal
): Promise<UsageTimeseriesResponse> {
  const query = new URLSearchParams();
  if (params.after) {
    query.set("after", params.after);
  }
  if (params.before) {
    query.set("before", params.before);
  }
  if (params.bucket) {
    query.set("bucket", params.bucket);
  }

  const qs = query.toString();
  const url = qs ? `/admin/v1/usage/timeseries?${qs}` : "/admin/v1/usage/timeseries";

  return adminFetch<UsageTimeseriesResponse>(url, {
    method: "GET",
    signal,
    validate: validateUsageTimeseriesResponse,
  });
}

export async function getUsageBreakdown(
  params: UsageBreakdownParams,
  signal?: AbortSignal
): Promise<UsageBreakdownResponse> {
  const query = new URLSearchParams();
  if (params.after) {
    query.set("after", params.after);
  }
  if (params.before) {
    query.set("before", params.before);
  }
  query.set("group_by", params.group_by);

  const qs = query.toString();
  const url = `/admin/v1/usage/breakdown?${qs}`;

  return adminFetch<UsageBreakdownResponse>(url, {
    method: "GET",
    signal,
    validate: validateUsageBreakdownResponse,
  });
}
