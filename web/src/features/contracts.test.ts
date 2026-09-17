import { describe, it, expect } from "vitest";
import keyListFixture from "./keys/fixtures/keyList.fixture.json";
import keyDetailFixture from "./keys/fixtures/keyDetail.fixture.json";
import createKeyFixture from "./keys/fixtures/createKey.fixture.json";
import requestListFixture from "./requests/fixtures/requestList.fixture.json";
import requestDetailFixture from "./requests/fixtures/requestDetail.fixture.json";
import requestBodyFixture from "./requests/fixtures/requestBody.fixture.json";
import readinessPassFixture from "./system/fixtures/readinessPass.fixture.json";
import readinessFailFixture from "./system/fixtures/readinessFail.fixture.json";

import {
  validateKeyListResponse,
  validateKeyDetail,
  validateCreateKeyResponse,
} from "./keys/validation";
import {
  validateRequestListResponse,
  validateRequestDetail,
  validateRequestBodyContent,
} from "./requests/validation";
import { validateReadinessResponse } from "./system/validation";
import { ValidationError } from "../shared/transport";

describe("Contract Fixtures and Runtime Validation", () => {
  describe("API Keys Contracts", () => {
    it("successfully decodes keyList fixture", () => {
      const decoded = validateKeyListResponse(keyListFixture);
      expect(decoded.keys).toHaveLength(2);
      expect(decoded.keys[0]!.id).toBe("key-0191eb0b62bc7b7489a2434685ef3b63");
      expect(decoded.keys[0]!.policy_summary.allow_models).toBe(true);
      expect(decoded.keys[1]!.expires_at).toBeNull();
      expect(decoded.next_cursor).toBeDefined();
    });

    it("successfully decodes keyDetail fixture", () => {
      const decoded = validateKeyDetail(keyDetailFixture);
      expect(decoded.id).toBe("key-0191eb0b62bc7b7489a2434685ef3b63");
      expect(decoded.policy.allowed_models).toEqual(["gpt-4o", "claude-3-5-sonnet"]);
      expect(decoded.policy.max_concurrent_requests).toBe(5);
      expect(decoded.policy.budget_limits[0]!.amount_micros).toBe(5000000);
    });

    it("successfully decodes createKey fixture", () => {
      const decoded = validateCreateKeyResponse(createKeyFixture);
      expect(decoded.id).toBe("key-0191eb0b62bc7b7489a2434685ef3b65");
      expect(decoded.key).toBe("sk-5ca932147ff0114e9870abcd1234efgh");
    });

    it("rejects malformed key payloads with ValidationError", () => {
      expect(() => validateKeyListResponse(null)).toThrow(ValidationError);
      expect(() => validateKeyListResponse({ keys: "not-array" })).toThrow(ValidationError);
      expect(() => validateKeyDetail({ name: "no-id" })).toThrow(ValidationError);
      expect(() => validateCreateKeyResponse({ id: "1" /* missing key */ })).toThrow(ValidationError);
    });
  });

  describe("Requests Contracts", () => {
    it("successfully decodes requestList fixture and preserves null vs zero", () => {
      const decoded = validateRequestListResponse(requestListFixture);
      expect(decoded.requests).toHaveLength(2);

      const first = decoded.requests[0]!;
      expect(first.downstream_status).toBe(200);
      expect(first.input_tokens).toBe(120);
      expect(first.reasoning_output_tokens).toBe(0); // Preserves exact 0
      expect(first.cost_micros).toBe(8250);

      const second = decoded.requests[1]!;
      expect(second.downstream_status).toBe(429);
      expect(second.upstream_status).toBeNull(); // Preserves null
      expect(second.input_tokens).toBeNull(); // Preserves null
      expect(second.cost_micros).toBeNull(); // Preserves null
    });

    it("successfully decodes requestDetail fixture with body kinds", () => {
      const decoded = validateRequestDetail(requestDetailFixture);
      expect(decoded.request_id).toBe(
        "0191eb0b62bc7b7489a2434685ef3b600191eb0b62bc7b7489a2434685ef3b60"
      );
      expect(decoded.has_bodies).toEqual(["client_request", "response"]);
    });

    it("successfully parses request body content with headers", () => {
      const headers = new Headers({
        "Content-Type": requestBodyFixture.content_type,
        "X-Original-Size": String(requestBodyFixture.original_size),
        "X-Truncated": String(requestBodyFixture.truncated),
      });

      const body = validateRequestBodyContent(
        requestBodyFixture.request_id,
        "client_request",
        { status: 200, headers, data: requestBodyFixture.data }
      );

      expect(body.request_id).toBe(requestBodyFixture.request_id);
      expect(body.kind).toBe("client_request");
      expect(body.original_size).toBe(256);
      expect(body.truncated).toBe(false);
      expect(body.content_type).toBe("application/json");
      expect(body.data).toBe(requestBodyFixture.data);
    });

    it("rejects malformed request payloads", () => {
      expect(() => validateRequestListResponse({ requests: [{ model: "gpt" }] })).toThrow(
        ValidationError
      );
      expect(() => validateRequestDetail({ not_an_id: 123 })).toThrow(ValidationError);
    });
  });

  describe("System Readiness Contracts", () => {
    it("successfully decodes healthy readiness response", () => {
      const decoded = validateReadinessResponse(readinessPassFixture);
      expect(decoded.ready).toBe(true);
      expect(decoded.checks.database?.status).toBe("pass");
      expect(decoded.version).toBe("v0.1.0-rc1");
    });

    it("successfully decodes failing readiness response with check message", () => {
      const decoded = validateReadinessResponse(readinessFailFixture);
      expect(decoded.ready).toBe(false);
      expect(decoded.checks.database?.status).toBe("fail");
      expect(decoded.checks.database?.message).toBe("readiness database message");
      expect(decoded.checks.lifecycle?.status).toBe("fail");
      expect(decoded.checks.lifecycle?.message).toBe("gateway is draining");
    });

    it("rejects invalid readiness responses", () => {
      expect(() => validateReadinessResponse({ ready: "not-bool" })).toThrow(ValidationError);
      expect(() => validateReadinessResponse(null)).toThrow(ValidationError);
    });
  });
});
