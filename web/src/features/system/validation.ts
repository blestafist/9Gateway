import { ValidationError } from "../../shared/transport";
import { ReadinessCheck, ReadinessResponse } from "./types";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function validateReadinessCheck(name: string, raw: unknown): ReadinessCheck {
  if (!isObject(raw)) {
    throw new ValidationError(`Readiness check ${name} must be an object`);
  }

  return {
    name: typeof raw.name === "string" ? raw.name : name,
    status: typeof raw.status === "string" ? raw.status : "fail",
    message: typeof raw.message === "string" ? raw.message : undefined,
  };
}

export function validateReadinessResponse(raw: unknown): ReadinessResponse {
  if (!isObject(raw)) {
    throw new ValidationError("Readiness response must be an object");
  }

  if (typeof raw.ready !== "boolean") {
    throw new ValidationError("Readiness response ready field must be a boolean");
  }

  const checks: Record<string, ReadinessCheck> = {};
  if (isObject(raw.checks)) {
    for (const [key, value] of Object.entries(raw.checks)) {
      checks[key] = validateReadinessCheck(key, value);
    }
  }

  return {
    ready: raw.ready,
    checks,
    version: typeof raw.version === "string" ? raw.version : "",
    commit: typeof raw.commit === "string" ? raw.commit : "",
  };
}
