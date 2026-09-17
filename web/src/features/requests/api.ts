import { adminFetch, RawResponseResult } from "../../shared/transport";
import {
  AdminRequestDetail,
  AdminRequestListResponse,
  RequestBodyContent,
  RequestBodyKind,
  RequestListFilters,
} from "./types";
import {
  validateRequestBodyContent,
  validateRequestDetail,
  validateRequestListResponse,
} from "./validation";

export async function listRequests(
  filters: RequestListFilters = {},
  signal?: AbortSignal
): Promise<AdminRequestListResponse> {
  const params = new URLSearchParams();
  if (filters.limit) {
    params.set("limit", String(filters.limit));
  }
  if (filters.cursor) {
    params.set("cursor", filters.cursor);
  }
  if (filters.key_id) {
    params.set("key_id", filters.key_id);
  }
  if (filters.after) {
    params.set("after", filters.after);
  }
  if (filters.before) {
    params.set("before", filters.before);
  }

  const query = params.toString();
  const url = query ? `/admin/v1/requests?${query}` : "/admin/v1/requests";

  return adminFetch<AdminRequestListResponse>(url, {
    method: "GET",
    signal,
    validate: validateRequestListResponse,
  });
}

export async function getRequestDetail(
  id: string,
  signal?: AbortSignal
): Promise<AdminRequestDetail> {
  return adminFetch<AdminRequestDetail>(
    `/admin/v1/requests/${encodeURIComponent(id)}`,
    {
      method: "GET",
      signal,
      validate: validateRequestDetail,
    }
  );
}

export async function getRequestBody(
  id: string,
  kind: RequestBodyKind,
  signal?: AbortSignal
): Promise<RequestBodyContent> {
  const raw = await adminFetch<RawResponseResult>(
    `/admin/v1/requests/${encodeURIComponent(id)}/bodies/${encodeURIComponent(kind)}`,
    {
      method: "GET",
      responseType: "raw",
      signal,
    }
  );

  return validateRequestBodyContent(id, kind, raw);
}
