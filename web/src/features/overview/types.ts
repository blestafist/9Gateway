export interface OverviewAggregate {
  requests: number;
  total_requests: number;
  successful_requests: number;
  error_requests: number;
  rejected_requests: number;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  output_tokens: number | null;
  cost_micros: number | null;
}

export interface OverviewKeyCounts {
  total: number;
  enabled: number;
  total_keys: number;
  enabled_keys: number;
}

export interface OverviewRecentRequest {
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

export interface AdminOverviewResponse {
  current_range_start: string;
  current_range_end: string;
  previous_range_start: string;
  previous_range_end: string;
  data_timestamp: string;
  current: OverviewAggregate;
  previous: OverviewAggregate;
  requests: number;
  total_requests: number;
  successful_requests: number;
  error_requests: number;
  rejected_requests: number;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  output_tokens: number | null;
  cost_micros: number | null;
  active_requests: number;
  key_counts: OverviewKeyCounts;
  recent_requests: OverviewRecentRequest[];
}

export type OverviewPeriodPreset = "1h" | "24h" | "7d" | "30d";

export interface OverviewQueryParams {
  after?: string;
  before?: string;
  period?: OverviewPeriodPreset;
}
