import { ValidationError } from "../../shared/transport";
import { ReadinessCheck, ReadinessResponse } from "./types";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function validateReadinessCheck(name: string, raw: unknown): ReadinessCheck {
  if (!isObject(raw)) {
    throw new ValidationError(`Readiness check ${name} must be an object`);
  }

  if (typeof raw.name !== "string") {
    throw new ValidationError(`Readiness check ${name} name must be a string`);
  }

  if (typeof raw.status !== "string") {
    throw new ValidationError(`Readiness check ${name} status must be a string`);
  }

  if (raw.message !== undefined && raw.message !== null && typeof raw.message !== "string") {
    throw new ValidationError(`Readiness check ${name} message must be a string or undefined`);
  }

  return {
    name: raw.name,
    status: raw.status as ReadinessCheck["status"],
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

  if (!isObject(raw.checks)) {
    throw new ValidationError("Readiness response checks must be an object");
  }

  const checks: Record<string, ReadinessCheck> = {};
  for (const [key, value] of Object.entries(raw.checks)) {
    checks[key] = validateReadinessCheck(key, value);
  }

  if (typeof raw.version !== "string") {
    throw new ValidationError("Readiness response version must be a string");
  }

  if (typeof raw.commit !== "string") {
    throw new ValidationError("Readiness response commit must be a string");
  }

  return {
    ready: raw.ready,
    checks,
    version: raw.version,
    commit: raw.commit,
  };
}
