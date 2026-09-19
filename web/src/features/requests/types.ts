export type RequestBodyKind =
  | "client_request"
  | "upstream_request"
  | "response";

export interface AdminRequestListItem {
  request_id: string;
  api_key_id: string | null;
  api_key_name: string | null;
  method: string | null;
  path: string | null;
  route: string | null;
  model: string | null;
  requested_mode: string | null;
  upstream_mode: string | null;
  delivered_mode: string | null;
  downstream_status: number | null;
  upstream_status: number | null;
  terminal_outcome: string | null;
  upstream_started: boolean;
  error_code: string | null;
  client_bytes: number | null;
  upstream_bytes: number | null;
  delivered_bytes: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  cached_input_tokens: number | null;
  reasoning_output_tokens: number | null;
  cost_micros: number | null;
  started_at: string | null;
  upstream_started_at: string | null;
  upstream_headers_at: string | null;
  first_byte_at: string | null;
  finished_at: string | null;
  total_micros: number | null;
  time_to_upstream_headers_micros: number | null;
  time_to_first_byte_micros: number | null;
  stream_close_delay_micros: number | null;
}

export interface AdminRequestListResponse {
  requests: AdminRequestListItem[];
  next_cursor?: string;
}

export interface AdminRequestDetail extends AdminRequestListItem {
  has_bodies: RequestBodyKind[];
}

export interface RequestBodyContent {
  request_id: string;
  kind: RequestBodyKind;
  original_size: number;
  truncated: boolean;
  content_type: string;
  data: string;
  bytes?: Uint8Array;
}

export interface RequestListFilters {
  limit?: number;
  cursor?: string;
  key_id?: string;
  after?: string;
  before?: string;
}

export type RequestRangePreset =
  | "all"
  | "1h"
  | "24h"
  | "7d"
  | "30d"
  | "custom";

export interface RequestPageFilters {
  keyId: string;
  preset: RequestRangePreset;
  after?: string;
  before?: string;
  pageSize: number;
}

