export interface SafeErrorEnvelope {
  error: {
    message: string;
    type: string;
    code: string;
    param?: string;
  };
}

export class AdminApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly type: string;
  readonly param?: string;

  constructor(status: number, envelope?: SafeErrorEnvelope | null, fallbackMessage?: string) {
    const code = envelope?.error?.code || (status === 401 ? "invalid_api_key" : "admin_error");
    const type = envelope?.error?.type || "api_error";
    const message =
      envelope?.error?.message ||
      fallbackMessage ||
      `Admin API request failed with status ${status}`;

    super(message);
    this.name = "AdminApiError";
    this.status = status;
    this.code = code;
    this.type = type;
    this.param = envelope?.error?.param;
  }

  get isAuthError(): boolean {
    return this.status === 401;
  }

  get isNotFoundError(): boolean {
    return this.status === 404;
  }

  get isConflictError(): boolean {
    return this.status === 409;
  }

  get isRateLimitError(): boolean {
    return this.status === 429;
  }

  get isServerError(): boolean {
    return this.status >= 500;
  }

  get isCursorExpired(): boolean {
    return this.code === "cursor_expired";
  }
}

export class ValidationError extends Error {
  constructor(message: string, public readonly details?: unknown) {
    super(message);
    this.name = "ValidationError";
  }
}

export class OversizedResponseError extends Error {
  constructor(message: string, public readonly byteCount?: number, public readonly maxBytes?: number) {
    super(message);
    this.name = "OversizedResponseError";
  }
}

export class GenerationMismatchError extends Error {
  constructor(message = "Response discarded because session or filter generation changed") {
    super(message);
    this.name = "GenerationMismatchError";
  }
}

export class NetworkError extends Error {
  constructor(message: string, public readonly cause?: unknown) {
    super(message);
    this.name = "NetworkError";
  }
}

export class OfflineError extends Error {
  constructor(message = "Cannot perform network request while offline") {
    super(message);
    this.name = "OfflineError";
  }
}
