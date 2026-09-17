import { adminFetch } from "../../shared/transport";
import {
  AdminKeyDetail,
  AdminKeyListResponse,
  CreateAdminKeyRequest,
  CreateAdminKeyResponse,
  KeyListFilters,
  UpdateAdminKeyPolicyRequest,
} from "./types";
import {
  validateCreateKeyResponse,
  validateKeyDetail,
  validateKeyListResponse,
} from "./validation";

export async function listKeys(
  filters: KeyListFilters = {},
  signal?: AbortSignal
): Promise<AdminKeyListResponse> {
  const params = new URLSearchParams();
  if (filters.limit) {
    params.set("limit", String(filters.limit));
  }
  if (filters.cursor) {
    params.set("cursor", filters.cursor);
  }

  const query = params.toString();
  const url = query ? `/admin/v1/keys?${query}` : "/admin/v1/keys";

  return adminFetch<AdminKeyListResponse>(url, {
    method: "GET",
    signal,
    validate: validateKeyListResponse,
  });
}

export async function getKeyDetail(
  id: string,
  signal?: AbortSignal
): Promise<AdminKeyDetail> {
  return adminFetch<AdminKeyDetail>(`/admin/v1/keys/${encodeURIComponent(id)}`, {
    method: "GET",
    signal,
    validate: validateKeyDetail,
  });
}

export async function createKey(
  request: CreateAdminKeyRequest,
  signal?: AbortSignal
): Promise<CreateAdminKeyResponse> {
  return adminFetch<CreateAdminKeyResponse>("/admin/v1/keys", {
    method: "POST",
    body: request,
    signal,
    validate: validateCreateKeyResponse,
  });
}

export async function updateKeyPolicy(
  id: string,
  request: UpdateAdminKeyPolicyRequest,
  signal?: AbortSignal
): Promise<AdminKeyDetail> {
  return adminFetch<AdminKeyDetail>(
    `/admin/v1/keys/${encodeURIComponent(id)}/policy`,
    {
      method: "PUT",
      body: request,
      signal,
      validate: validateKeyDetail,
    }
  );
}
