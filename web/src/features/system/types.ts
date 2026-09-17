export interface ReadinessCheck {
  name: string;
  status: "pass" | "fail" | string;
  message?: string;
}

export interface ReadinessResponse {
  ready: boolean;
  checks: Record<string, ReadinessCheck>;
  version: string;
  commit: string;
}
