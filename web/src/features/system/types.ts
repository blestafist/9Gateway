export interface ReadinessCheck {
  name: string;
  status: "pass" | "fail" | string;
  message?: string;
}

export interface ReadinessSummary {
  ready: boolean;
  checks: Record<string, ReadinessCheck>;
}

export interface StorageSummary {
  status: "healthy" | "degraded" | "unavailable" | string;
  healthy: boolean;
  schema_version: number | null;
  current_schema_version: number;
}

export interface TelemetrySummary {
  queue_depth: number;
  queue_capacity: number;
  dropped_records: number;
}

export interface SystemLimits {
  request_retention_seconds: number;
  body_retention_seconds: number;
  max_captured_body_bytes: number;
}

export interface SystemResponse {
  version: string;
  commit: string;
  build_time: string;
  build_date: string;
  start_time: string;
  uptime_seconds: number;
  ready: boolean;
  readiness: ReadinessSummary;
  storage: StorageSummary;
  sqlite: StorageSummary;
  telemetry: TelemetrySummary;
  active_requests: number;
  limits: SystemLimits;
}

// Deprecated alias retained for backwards compatibility
export interface ReadinessResponse {
  ready: boolean;
  checks: Record<string, ReadinessCheck>;
  version: string;
  commit: string;
}
