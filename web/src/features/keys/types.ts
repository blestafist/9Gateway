export interface KeyPolicySummary {
  allow_models: boolean;
  deny_models: boolean;
  log_request_body: boolean;
  log_response_body: boolean;
}

export interface AdminKeyListItem {
  id: string;
  name: string;
  display_prefix: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  policy_summary: KeyPolicySummary;
}

export interface AdminKeyListResponse {
  keys: AdminKeyListItem[];
  next_cursor?: string;
}

export interface RequestPolicyWindow {
  amount: number;
  duration: number;
}

export interface TokenPolicyWindow {
  amount: number;
  duration: number;
}

export interface BudgetLimit {
  period: string;
  amount_micros: number;
}

export type TokenMode = "total" | "input_only" | "output_only" | string;

export interface AdminKeyPolicy {
  allowed_models: string[];
  denied_models: string[];
  request_windows: RequestPolicyWindow[];
  token_windows: TokenPolicyWindow[];
  token_mode: TokenMode;
  max_concurrent_requests: number;
  budget_limits: BudgetLimit[];
  log_request_body: boolean;
  log_response_body: boolean;
}

export interface AdminKeyDetail {
  id: string;
  name: string;
  display_prefix: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  policy: AdminKeyPolicy;
}

export interface CreateAdminKeyRequest {
  name: string;
  expires_at?: string | null;
}

export interface CreateAdminKeyResponse {
  id: string;
  name: string;
  prefix: string;
  enabled: boolean;
  expires_at?: string;
  created_at: string;
  key: string;
  policy: Record<string, unknown>;
}

export interface UpdateAdminKeyPolicyRequest {
  enabled: boolean;
  policy: Record<string, unknown>;
}

export interface KeyListFilters {
  limit?: number;
  cursor?: string;
}
