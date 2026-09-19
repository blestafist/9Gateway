import { describe, it, expect } from "vitest";
import keyListFixture from "./keys/fixtures/keyList.fixture.json";
import keyDetailFixture from "./keys/fixtures/keyDetail.fixture.json";
import createKeyFixture from "./keys/fixtures/createKey.fixture.json";
import requestListFixture from "./requests/fixtures/requestList.fixture.json";
import requestDetailFixture from "./requests/fixtures/requestDetail.fixture.json";
import requestBodyFixture from "./requests/fixtures/requestBody.fixture.json";
import readinessPassFixture from "./system/fixtures/readinessPass.fixture.json";
import readinessFailFixture from "./system/fixtures/readinessFail.fixture.json";
import overviewFixture from "./overview/fixtures/overview.fixture.json";
import overviewNullsFixture from "./overview/fixtures/overviewNulls.fixture.json";
import overviewEmptyFixture from "./overview/fixtures/overviewEmpty.fixture.json";

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
import { validateOverviewResponse } from "./overview/validation";
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

    it("rejects string 'false' or non-boolean values passed to boolean fields", () => {
      expect(() =>
        validateKeyListResponse({
          keys: [{ ...keyListFixture.keys[0], enabled: "false" }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyListResponse({
          keys: [
            {
              ...keyListFixture.keys[0],
              policy_summary: {
                ...keyListFixture.keys[0]!.policy_summary,
                allow_models: "false",
              },
            },
          ],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          enabled: "false",
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            log_request_body: "false",
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            log_response_body: "true",
          },
        })
      ).toThrow(ValidationError);
    });

    it("rejects numeric string '123' passed as a number field in key policy", () => {
      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            max_concurrent_requests: "123",
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            request_windows: [{ amount: "123", duration: 60 }],
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            request_windows: [{ amount: 60, duration: "60" }],
          },
        })
      ).toThrow(ValidationError);

       expect(
         validateKeyDetail({
           ...keyDetailFixture,
           policy: {
             ...keyDetailFixture.policy,
             token_windows: [{ amount: "1000", duration: 3600 }],
           },
         }).policy.token_windows[0]?.amount
       ).toBe(1000);

       expect(
         validateKeyDetail({
           ...keyDetailFixture,
           policy: {
             ...keyDetailFixture.policy,
             budget_limits: [{ period: "daily", amount_micros: "5000000" }],
           },
         }).policy.budget_limits[0]?.amount_micros
       ).toBe(5000000);

       expect(() =>
         validateKeyDetail({
           ...keyDetailFixture,
           policy: {
             ...keyDetailFixture.policy,
             token_windows: [{ amount: Number.MAX_SAFE_INTEGER + 1, duration: 3600 }],
           },
         })
       ).toThrow(ValidationError);
     });

    it("rejects missing required key-detail fields", () => {
      const base = { ...keyDetailFixture };

      const withoutId = { ...base };
      delete (withoutId as { id?: string }).id;
      expect(() => validateKeyDetail(withoutId)).toThrow(ValidationError);

      const withoutName = { ...base };
      delete (withoutName as { name?: string }).name;
      expect(() => validateKeyDetail(withoutName)).toThrow(ValidationError);

      const withoutPrefix = { ...base };
      delete (withoutPrefix as { display_prefix?: string }).display_prefix;
      expect(() => validateKeyDetail(withoutPrefix)).toThrow(ValidationError);

      const withoutEnabled = { ...base };
      delete (withoutEnabled as { enabled?: boolean }).enabled;
      expect(() => validateKeyDetail(withoutEnabled)).toThrow(ValidationError);

      const withoutCreatedAt = { ...base };
      delete (withoutCreatedAt as { created_at?: string }).created_at;
      expect(() => validateKeyDetail(withoutCreatedAt)).toThrow(ValidationError);

      const withoutUpdatedAt = { ...base };
      delete (withoutUpdatedAt as { updated_at?: string }).updated_at;
      expect(() => validateKeyDetail(withoutUpdatedAt)).toThrow(ValidationError);

      const withoutPolicy = { ...base };
      delete (withoutPolicy as { policy?: unknown }).policy;
      expect(() => validateKeyDetail(withoutPolicy)).toThrow(ValidationError);
    });

    it("rejects malformed nested policy values", () => {
      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            allowed_models: [123],
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            denied_models: "not-array",
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            request_windows: ["not-an-object"],
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            budget_limits: [{ period: 123, amount_micros: 5000 }],
          },
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateKeyDetail({
          ...keyDetailFixture,
          policy: {
            ...keyDetailFixture.policy,
            token_mode: 999,
          },
        })
      ).toThrow(ValidationError);
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

    it("rejects string 'false' or truthy non-boolean for upstream_started", () => {
      const baseRequest = requestListFixture.requests[0];
      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, upstream_started: "false" }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, upstream_started: "true" }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, upstream_started: 1 }],
        })
      ).toThrow(ValidationError);
    });

    it("rejects numeric strings passed to number fields in requests", () => {
      const baseRequest = requestListFixture.requests[0];

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, downstream_status: "200" }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, client_bytes: "1024" }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, input_tokens: "120" }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, total_micros: "1200000" }],
        })
      ).toThrow(ValidationError);
    });

    it("rejects non-string values passed to optional string fields in requests", () => {
      const baseRequest = requestListFixture.requests[0];

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, model: 12345 }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, path: true }],
        })
      ).toThrow(ValidationError);

      expect(() =>
        validateRequestListResponse({
          requests: [{ ...baseRequest, terminal_outcome: 42 }],
        })
      ).toThrow(ValidationError);
    });
  });

  describe("System Readiness Contracts", () => {
    it("successfully decodes healthy readiness response", () => {
      const decoded = validateReadinessResponse(readinessPassFixture);
      expect(decoded.ready).toBe(true);
      expect(decoded.checks.sqlite?.status).toBe("pass");
      expect(decoded.version).toBe("v0.1.0-rc1");
    });

    it("successfully decodes failing readiness response with check message", () => {
      const decoded = validateReadinessResponse(readinessFailFixture);
      expect(decoded.ready).toBe(false);
      expect(decoded.checks.sqlite?.status).toBe("fail");
      expect(decoded.checks.sqlite?.message).toBe("readiness sqlite message");
      expect(decoded.checks.lifecycle?.status).toBe("fail");
      expect(decoded.checks.lifecycle?.message).toBe("gateway is draining");
    });

    it("rejects invalid readiness responses", () => {
      expect(() => validateReadinessResponse({ ready: "not-bool" })).toThrow(ValidationError);
      expect(() => validateReadinessResponse(null)).toThrow(ValidationError);
    });
  });

  describe("Overview Contracts", () => {
    it("successfully decodes full overview fixture", () => {
      const decoded = validateOverviewResponse(overviewFixture);
      expect(decoded.requests).toBe(150);
      expect(decoded.total_requests).toBe(150);
      expect(decoded.successful_requests).toBe(142);
      expect(decoded.error_requests).toBe(5);
      expect(decoded.rejected_requests).toBe(3);
      expect(decoded.input_tokens).toBe(19178344);
      expect(decoded.cached_input_tokens).toBe(11381601);
      expect(decoded.output_tokens).toBe(72803);
      expect(decoded.cost_micros).toBe(27990000);
      expect(decoded.active_requests).toBe(2);
      expect(decoded.key_counts.total).toBe(6);
      expect(decoded.key_counts.enabled).toBe(5);
      expect(decoded.recent_requests).toHaveLength(2);
      expect(decoded.recent_requests[0]?.request_id).toBe(
        "0191eb0b62bc7b7489a2434685ef3b600191eb0b62bc7b7489a2434685ef3b60"
      );
      expect(decoded.recent_requests[0]?.upstream_started).toBe(true);
      expect(decoded.recent_requests[1]?.terminal_outcome).toBe("pre_upstream");
      expect(decoded.recent_requests[1]?.upstream_started).toBe(false);
    });

    it("successfully decodes overviewNulls fixture preserving null usage/cost", () => {
      const decoded = validateOverviewResponse(overviewNullsFixture);
      expect(decoded.requests).toBe(50);
      expect(decoded.current.input_tokens).toBeNull();
      expect(decoded.current.cached_input_tokens).toBeNull();
      expect(decoded.current.output_tokens).toBeNull();
      expect(decoded.current.cost_micros).toBeNull();
      expect(decoded.previous.input_tokens).toBeNull();
      expect(decoded.previous.cost_micros).toBeNull();
      expect(decoded.recent_requests).toHaveLength(0);
    });

    it("successfully decodes overviewEmpty fixture with zero baseline", () => {
      const decoded = validateOverviewResponse(overviewEmptyFixture);
      expect(decoded.requests).toBe(0);
      expect(decoded.current.successful_requests).toBe(0);
      expect(decoded.current.input_tokens).toBe(0);
      expect(decoded.current.cost_micros).toBe(0);
      expect(decoded.active_requests).toBe(0);
      expect(decoded.key_counts.total).toBe(3);
      expect(decoded.recent_requests).toHaveLength(0);
    });

    it("rejects malformed overview responses with ValidationError", () => {
      expect(() => validateOverviewResponse(null)).toThrow(ValidationError);
      expect(() => validateOverviewResponse("not-object")).toThrow(ValidationError);
      expect(() =>
        validateOverviewResponse({
          ...overviewFixture,
          current_range_start: 12345, // string required
        })
      ).toThrow(ValidationError);
      expect(() =>
        validateOverviewResponse({
          ...overviewFixture,
          current: { ...overviewFixture.current, total_requests: "100" }, // number required
        })
      ).toThrow(ValidationError);
      expect(() =>
        validateOverviewResponse({
          ...overviewFixture,
          key_counts: { total: "five", enabled: 1 },
        })
      ).toThrow(ValidationError);
      expect(() =>
        validateOverviewResponse({
          ...overviewFixture,
          recent_requests: "not-array",
        })
      ).toThrow(ValidationError);
      expect(() =>
        validateOverviewResponse({
          ...overviewFixture,
          recent_requests: [{ ...overviewFixture.recent_requests[0], upstream_started: "yes" }],
        })
      ).toThrow(ValidationError);
    });
  });
});
