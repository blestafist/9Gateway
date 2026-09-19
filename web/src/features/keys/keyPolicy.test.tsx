import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { AdminQueryProvider, createAdminQueryClient } from "../../shared/query";
import { AdminKeyDetail } from "./types";
import { KeyPolicyForm } from "./components/KeyPolicyForm";
import { KeyDetailDrawer } from "./components/KeyDetailDrawer";
import {
  dollarsStringToMicros,
  microsToDollarsString,
  secondsToDurationInput,
  durationInputToSeconds,
  parseDurationToSeconds,
  validatePolicyForm,
  detectSecuritySensitiveChanges,
  reorderItem,
  policyToFormValues,
  formValuesToPolicyPayload,
} from "./policyHelpers";

function mockJsonResponse(data: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(data), {
    status: init?.status ?? 200,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
}

const baseKeyDetail: AdminKeyDetail = {
  id: "key-test-policy-123",
  name: "Policy Test Key",
  display_prefix: "sk-pol1",
  enabled: true,
  created_at: "2026-09-01T12:00:00Z",
  updated_at: "2026-09-01T12:00:00Z",
  expires_at: "2030-01-01T00:00:00Z",
  policy: {
    allowed_models: ["gpt-4o", "claude-3-5-sonnet"],
    denied_models: ["gpt-4-internal"],
    request_windows: [{ amount: 60, duration: 60 }],
    token_windows: [{ amount: 100000, duration: 3600 }],
    token_mode: "estimate",
    max_concurrent_requests: 5,
    budget_limits: [{ period: "day", amount_micros: 5000000 }],
    log_request_body: false,
    log_response_body: false,
  },
};

describe("T172 Key Policy Editor & Controls", () => {
  describe("Policy Conversion & Math Helpers", () => {
    it("losslessly converts micro-dollars to and from human dollar strings", () => {
      // Whole dollars
      expect(microsToDollarsString(5_000_000)).toBe("5");
      expect(dollarsStringToMicros("5")).toBe(5_000_000);
      expect(dollarsStringToMicros("5.00")).toBe(5_000_000);
      expect(dollarsStringToMicros("$5.00")).toBe(5_000_000);

      // Fractional dollars with exact precision up to 6 decimal places
      expect(microsToDollarsString(10_500_000)).toBe("10.5");
      expect(dollarsStringToMicros("10.5")).toBe(10_500_000);
      expect(dollarsStringToMicros("10.50")).toBe(10_500_000);

      // Micro-fraction: 42 µ$ ($0.000042)
      expect(microsToDollarsString(42)).toBe("0.000042");
      expect(dollarsStringToMicros("0.000042")).toBe(42);

      // 1 µ$ ($0.000001)
      expect(microsToDollarsString(1)).toBe("0.000001");
      expect(dollarsStringToMicros("0.000001")).toBe(1);

      // Zero & negative / invalid
      expect(microsToDollarsString(0)).toBe("0");
      expect(dollarsStringToMicros("0")).toBe(0);
      expect(dollarsStringToMicros("invalid")).toBeNull();
      expect(dollarsStringToMicros("1.1234567")).toBeNull(); // >6 decimal places
    });

    it("losslessly converts duration seconds to and from human amount/unit pairs", () => {
      // Days
      expect(secondsToDurationInput(86400)).toEqual({ amount: 1, unit: "d" });
      expect(durationInputToSeconds(1, "d")).toBe(86400);

      // Hours
      expect(secondsToDurationInput(7200)).toEqual({ amount: 2, unit: "h" });
      expect(durationInputToSeconds(2, "h")).toBe(7200);

      // Minutes
      expect(secondsToDurationInput(60)).toEqual({ amount: 1, unit: "m" });
      expect(secondsToDurationInput(300)).toEqual({ amount: 5, unit: "m" });
      expect(durationInputToSeconds(5, "m")).toBe(300);

      // Non-multiple seconds (e.g. 90s)
      expect(secondsToDurationInput(90)).toEqual({ amount: 90, unit: "s" });
      expect(durationInputToSeconds(90, "s")).toBe(90);
    });

    it("parses Go duration strings into exact seconds and rejects numeric strings without unit", () => {
      expect(parseDurationToSeconds(60)).toBe(60);
      expect(parseDurationToSeconds("60s")).toBe(60);
      expect(parseDurationToSeconds("1m")).toBe(60);
      expect(parseDurationToSeconds("2h")).toBe(7200);
      expect(parseDurationToSeconds("1d")).toBe(86400);

      // Plain numeric strings or invalid strings must throw ValidationError
      expect(() => parseDurationToSeconds("60")).toThrow();
      expect(() => parseDurationToSeconds("bad")).toThrow();
      expect(() => parseDurationToSeconds(-5)).toThrow();
    });

    it("reorders list items immutably", () => {
      const original = ["a", "b", "c"];
      expect(reorderItem(original, 1, "up")).toEqual(["b", "a", "c"]);
      expect(reorderItem(original, 1, "down")).toEqual(["a", "c", "b"]);
      expect(reorderItem(original, 0, "up")).toEqual(["a", "b", "c"]); // boundary
      expect(reorderItem(original, 2, "down")).toEqual(["a", "b", "c"]); // boundary
      expect(original).toEqual(["a", "b", "c"]); // immutability preserved
    });

    it("round-trips complex policy between AdminKeyDetail and form payload without drift", () => {
      const formValues = policyToFormValues(baseKeyDetail);
      const payload = formValuesToPolicyPayload(formValues);

      expect(payload.enabled).toBe(true);
      expect(payload.policy.allowed_models).toEqual(["gpt-4o", "claude-3-5-sonnet"]);
      expect(payload.policy.denied_models).toEqual(["gpt-4-internal"]);
      expect(payload.policy.max_concurrent_requests).toBe(5);
      expect(payload.policy.token_mode).toBe("estimate");
      expect(payload.policy.request_windows).toEqual([{ amount: 60, duration: "60s" }]);
      expect(payload.policy.token_windows).toEqual([{ amount: 100000, duration: "3600s" }]);
      expect(payload.policy.budget_limits).toEqual([{ period: "day", amount_micros: 5000000 }]);
      expect(payload.policy.log_request_body).toBe(false);
      expect(payload.policy.log_response_body).toBe(false);
    });

    it("validates form values and rejects duplicate patterns, invalid ranges, and non-positive numbers", () => {
      const formValues = policyToFormValues(baseKeyDetail);

      // Add duplicate allowed model
      formValues.allowed_models.push("gpt-4o");
      // Add empty denied model
      formValues.denied_models.push("   ");
      // Add duplicate window duration
      formValues.request_windows.push({
        id: "test-dup-w",
        amount: 10,
        durationValue: 1,
        durationUnit: "m",
      });
      // Set concurrency to invalid 0 when in custom mode
      formValues.concurrency_mode = "custom";
      formValues.max_concurrent_requests = 0;
      // Add duplicate budget period
      formValues.budget_limits.push({
        id: "test-dup-b",
        period: "day",
        amount: "20.00",
        unit: "usd",
      });

      const errors = validatePolicyForm(formValues);
      expect(errors.summary.length).toBeGreaterThanOrEqual(4);
      expect(errors.summary.some((m) => m.includes("Duplicate allowed model"))).toBe(true);
      expect(errors.summary.some((m) => m.includes("cannot be empty"))).toBe(true);
      expect(errors.summary.some((m) => m.includes("Duplicate request window duration"))).toBe(true);
      expect(errors.summary.some((m) => m.includes("Max concurrent requests must be an integer"))).toBe(true);
      expect(errors.summary.some((m) => m.includes("Duplicate budget limit period"))).toBe(true);
    });

    it("detects security-sensitive changes when policy expands or bodies are captured", () => {
      const formValues = policyToFormValues(baseKeyDetail);

      // No changes initially
      expect(detectSecuritySensitiveChanges(baseKeyDetail, formValues)).toHaveLength(0);

      // Disable key
      formValues.enabled = false;
      let changes = detectSecuritySensitiveChanges(baseKeyDetail, formValues);
      expect(changes.some((c) => c.id === "disabled")).toBe(true);

      // Enable request and response body logging
      formValues.log_request_body = true;
      formValues.log_response_body = true;
      changes = detectSecuritySensitiveChanges(baseKeyDetail, formValues);
      expect(changes.some((c) => c.id === "log_req")).toBe(true);
      expect(changes.some((c) => c.id === "log_res")).toBe(true);

      // Remove allowlist (empty = all allowed)
      formValues.allowed_models = [];
      changes = detectSecuritySensitiveChanges(baseKeyDetail, formValues);
      expect(changes.some((c) => c.id === "allowlist_removed")).toBe(true);
    });
  });

  describe("KeyPolicyForm Component Lifecycle & User Workflows", () => {
    let queryClient: ReturnType<typeof createAdminQueryClient>;

    beforeEach(() => {
      queryClient = createAdminQueryClient();
      vi.restoreAllMocks();
    });

    const renderForm = (props?: {
      keyDetail?: AdminKeyDetail;
      onSuccess?: (updated: AdminKeyDetail) => void;
      onCancel?: () => void;
      onDirtyChange?: (isDirty: boolean) => void;
      readOnly?: boolean;
    }) => {
      return render(
        <AdminQueryProvider client={queryClient}>
          <KeyPolicyForm
            keyDetail={props?.keyDetail || baseKeyDetail}
            onSuccess={props?.onSuccess}
            onCancel={props?.onCancel}
            onDirtyChange={props?.onDirtyChange}
            readOnly={props?.readOnly}
          />
        </AdminQueryProvider>
      );
    };

    it("renders policy form with initial values and inline semantics explanations", () => {
      renderForm();

      // Semantics: allow-vs-deny precedence
      expect(screen.getByText(/Deny rules take absolute precedence over allow rules/i)).toBeInTheDocument();

      // Semantics: body logging sensitivity
      expect(screen.getByText(/Security & Data Privacy Sensitivity/i)).toBeInTheDocument();

      // Semantics: budget limits
      expect(screen.getByText(/Total limits lifetime spend; Day and Month reset at UTC/i)).toBeInTheDocument();

      // Form inputs
      expect(screen.getByDisplayValue("gpt-4o")).toBeInTheDocument();
      expect(screen.getByDisplayValue("claude-3-5-sonnet")).toBeInTheDocument();
      expect(screen.getByDisplayValue("gpt-4-internal")).toBeInTheDocument();
      expect(screen.getByTestId("policy-enabled-switch")).toBeInTheDocument();
      expect(screen.getByTestId("policy-expiry-value")).toHaveTextContent(/2030/);

      // Save button starts disabled until dirty
      const saveBtn = screen.getByTestId("save-policy-btn");
      expect(saveBtn).toBeDisabled();
    });

    it("supports adding, reordering, and removing model patterns", async () => {
      renderForm();

      // Add allowed pattern
      const addBtn = screen.getByTestId("add-allowed-model-btn");
      fireEvent.click(addBtn);

      const newInput = screen.getByTestId("allowed-model-input-2");
      expect(newInput).toBeInTheDocument();
      fireEvent.change(newInput, { target: { value: "gemini-1.5-pro" } });

      // Save button is now enabled
      expect(screen.getByTestId("save-policy-btn")).not.toBeDisabled();

      // Move pattern up
      const moveUpBtn = screen.getByTestId("allowed-model-up-1");
      fireEvent.click(moveUpBtn);
      // Index 0 should now be claude-3-5-sonnet
      expect(screen.getByTestId("allowed-model-input-0")).toHaveValue("claude-3-5-sonnet");

      // Remove newly added pattern
      const removeBtn = screen.getByTestId("allowed-model-remove-2");
      fireEvent.click(removeBtn);
      expect(screen.queryByTestId("allowed-model-input-2")).not.toBeInTheDocument();
    });

    it("supports adding, editing, and removing rate limits and budget limits", async () => {
      renderForm();

      // Add request window
      const addReqBtn = screen.getByTestId("add-req-window-btn");
      fireEvent.click(addReqBtn);
      expect(screen.getByTestId("req-window-row-1")).toBeInTheDocument();

      // Add budget limit
      const addBudgetBtn = screen.getByTestId("add-budget-limit-btn");
      fireEvent.click(addBudgetBtn);
      expect(screen.getByTestId("budget-row-1")).toBeInTheDocument();

      // Change budget amount and unit
      const budgetAmountInput = screen.getByTestId("budget-amount-1");
      fireEvent.change(budgetAmountInput, { target: { value: "25.00" } });
      expect(budgetAmountInput).toHaveValue("25.00");
    });

    it("validates fields on submit, displays summary banner, and focuses first error", async () => {
      renderForm();

      // Add an empty allowed model pattern
      const addBtn = screen.getByTestId("add-allowed-model-btn");
      fireEvent.click(addBtn);

      // Attempt save
      const saveBtn = screen.getByTestId("save-policy-btn");
      fireEvent.click(saveBtn);

      // Summary banner appears
      await waitFor(() => {
        expect(screen.getByTestId("policy-validation-summary")).toBeInTheDocument();
        expect(screen.getAllByText(/Allowed model pattern #3 cannot be empty/)).toHaveLength(2);
      });

      // Errored input gets focused
      const erroredInput = screen.getByTestId("allowed-model-input-2");
      await waitFor(() => {
        expect(document.activeElement).toBe(erroredInput);
      });
    });

    it("shows security review summary before saving security-sensitive changes", async () => {
      const mockPut = vi.fn().mockImplementation(async () =>
        mockJsonResponse({
          ...baseKeyDetail,
          policy: {
            ...baseKeyDetail.policy,
            log_request_body: true,
          },
        })
      );
      globalThis.fetch = mockPut;

      renderForm();

      // Turn on request body logging (security-sensitive)
      const logSwitch = screen.getByTestId("log-request-body-switch");
      fireEvent.click(logSwitch);

      // Click save
      const saveBtn = screen.getByTestId("save-policy-btn");
      fireEvent.click(saveBtn);

      // Review dialog opens
      await waitFor(() => {
        expect(screen.getByTestId("security-review-list")).toBeInTheDocument();
        expect(screen.getByText("Request Body Logging Enabled")).toBeInTheDocument();
      });

      // Confirm in dialog
      const confirmBtn = screen.getByTestId("review-dialog-confirm-btn");
      fireEvent.click(confirmBtn);

      await waitFor(() => {
        expect(mockPut).toHaveBeenCalledWith(
          "/admin/v1/keys/key-test-policy-123/policy",
          expect.objectContaining({
            method: "PUT",
          })
        );
      });
    });

    it("submits exactly one full replacement PUT request and updates server source of truth", async () => {
      const updatedKey: AdminKeyDetail = {
        ...baseKeyDetail,
        updated_at: "2026-09-02T10:00:00Z",
        policy: {
          ...baseKeyDetail.policy,
          max_concurrent_requests: 10,
        },
      };

      const mockPut = vi.fn().mockImplementation(async () => mockJsonResponse(updatedKey));
      globalThis.fetch = mockPut;

      const onSuccess = vi.fn();
      renderForm({ onSuccess });

      // Change concurrency (not security sensitive)
      const concurrencyInput = screen.getByTestId("max-concurrency-input");
      fireEvent.change(concurrencyInput, { target: { value: "10" } });

      const saveBtn = screen.getByTestId("save-policy-btn");
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(mockPut).toHaveBeenCalledTimes(1);
        expect(screen.getByTestId("policy-success-alert")).toBeInTheDocument();
        expect(onSuccess).toHaveBeenCalledWith(updatedKey);
      });
    });

    it("preserves edits on 409 conflict and provides a safe retry path", async () => {
      let callCount = 0;
      globalThis.fetch = vi.fn().mockImplementation(async () => {
        callCount++;
        if (callCount === 1) {
          return mockJsonResponse(
            { error: { message: "active token usage prevents this policy change", code: "conflict" } },
            { status: 409 }
          );
        }
        return mockJsonResponse({
          ...baseKeyDetail,
          policy: { ...baseKeyDetail.policy, max_concurrent_requests: 8 },
        });
      });

      renderForm();

      // Change concurrency to 8
      const concurrencyInput = screen.getByTestId("max-concurrency-input");
      fireEvent.change(concurrencyInput, { target: { value: "8" } });

      const saveBtn = screen.getByTestId("save-policy-btn");
      fireEvent.click(saveBtn);

      // Conflict alert appears
      await waitFor(() => {
        expect(screen.getByTestId("conflict-409-alert")).toBeInTheDocument();
        expect(screen.getByText(/Active token usage prevents this policy change/)).toBeInTheDocument();
      });

      // User's edit is preserved in input!
      expect(screen.getByTestId("max-concurrency-input")).toHaveValue(8);

      // Safe retry button is available
      const retryBtn = screen.getByTestId("conflict-retry-btn");
      fireEvent.click(retryBtn);

      await waitFor(() => {
        expect(screen.getByTestId("policy-success-alert")).toBeInTheDocument();
      });
      expect(callCount).toBe(2);
    });

    it("detects concurrent background server modification and presents conflict/reload decision", async () => {
      const { rerender } = render(
        <AdminQueryProvider client={queryClient}>
          <KeyPolicyForm keyDetail={baseKeyDetail} />
        </AdminQueryProvider>
      );

      // Make a local edit to make form dirty
      const concurrencyInput = screen.getByTestId("max-concurrency-input");
      fireEvent.change(concurrencyInput, { target: { value: "9" } });

      // Simulate a background refetch bringing newer updated_at timestamp
      const serverUpdatedKey: AdminKeyDetail = {
        ...baseKeyDetail,
        updated_at: "2026-09-03T15:30:00Z",
        policy: {
          ...baseKeyDetail.policy,
          max_concurrent_requests: 20,
        },
      };

      rerender(
        <AdminQueryProvider client={queryClient}>
          <KeyPolicyForm keyDetail={serverUpdatedKey} />
        </AdminQueryProvider>
      );

      // Concurrent modification banner appears
      await waitFor(() => {
        expect(screen.getByTestId("server-conflict-alert")).toBeInTheDocument();
      });

      // User chooses to Reload Server Policy
      const reloadBtn = screen.getByTestId("reload-server-policy-btn");
      fireEvent.click(reloadBtn);

      // Form updates to server value and clears dirty state
      expect(screen.getByTestId("max-concurrency-input")).toHaveValue(20);
      expect(screen.getByTestId("save-policy-btn")).toBeDisabled();
      expect(screen.queryByTestId("server-conflict-alert")).not.toBeInTheDocument();
    });

    it("warns before discarding dirty form edits via confirmation dialog", async () => {
      const onCancel = vi.fn();
      renderForm({ onCancel });

      // Change input to make form dirty
      const concurrencyInput = screen.getByTestId("max-concurrency-input");
      fireEvent.change(concurrencyInput, { target: { value: "12" } });

      // Click Discard Changes
      const cancelBtn = screen.getByTestId("cancel-policy-btn");
      fireEvent.click(cancelBtn);

      // Discard confirmation dialog opens
      await waitFor(() => {
        expect(screen.getByText("Discard Unsaved Changes?")).toBeInTheDocument();
      });

      // Cancel discard -> stays on form
      const keepBtn = screen.getByTestId("discard-dialog-cancel-btn");
      fireEvent.click(keepBtn);
      expect(onCancel).not.toHaveBeenCalled();

      // Click discard again and confirm
      fireEvent.click(cancelBtn);
      const confirmDiscardBtn = screen.getByTestId("discard-dialog-confirm-btn");
      fireEvent.click(confirmDiscardBtn);
      expect(onCancel).toHaveBeenCalled();
    });
  });

  describe("KeyDetailDrawer Composition & Modes", () => {
    let queryClient: ReturnType<typeof createAdminQueryClient>;

    beforeEach(() => {
      queryClient = createAdminQueryClient();
      vi.restoreAllMocks();
      globalThis.fetch = vi.fn().mockImplementation(async () =>
        mockJsonResponse(baseKeyDetail)
      );
    });

    it("opens in view mode and transitions seamlessly to edit mode", async () => {
      const onClose = vi.fn();
      render(
        <AdminQueryProvider client={queryClient}>
          <KeyDetailDrawer keyId="key-test-policy-123" isOpen={true} onClose={onClose} />
        </AdminQueryProvider>
      );

      await waitFor(() => {
        expect(screen.getByRole("heading", { name: "Identity" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Status & Expiry" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Policy Summary" })).toBeInTheDocument();
      });

      // Click Edit Policy button
      const editBtn = screen.getByTestId("edit-policy-btn");
      fireEvent.click(editBtn);

      // Transitions to structured policy form
      await waitFor(() => {
        expect(screen.getByRole("heading", { name: "Policy Configuration" })).toBeInTheDocument();
        expect(screen.getByTestId("key-policy-form")).toBeInTheDocument();
      });
    });

    it("intercepts drawer close when policy form has dirty edits", async () => {
      const onClose = vi.fn();
      render(
        <AdminQueryProvider client={queryClient}>
          <KeyDetailDrawer
            keyId="key-test-policy-123"
            isOpen={true}
            onClose={onClose}
            initialMode="edit"
          />
        </AdminQueryProvider>
      );

      await waitFor(() => {
        expect(screen.getByTestId("key-policy-form")).toBeInTheDocument();
      });

      // Modify a value
      const concurrencyInput = screen.getByTestId("max-concurrency-input");
      fireEvent.change(concurrencyInput, { target: { value: "99" } });

      // Attempt to close drawer via X / backdrop
      const closeBtn = screen.getByRole("button", { name: "Close drawer" });
      fireEvent.click(closeBtn);

      // Drawer close is blocked by discard confirmation dialog
      await waitFor(() => {
        expect(screen.getByTestId("drawer-discard-cancel-btn")).toBeInTheDocument();
      });
      expect(onClose).not.toHaveBeenCalled();

      // Confirm discard -> closes drawer
      const confirmBtn = screen.getByTestId("drawer-discard-confirm-btn");
      fireEvent.click(confirmBtn);
      expect(onClose).toHaveBeenCalled();
    });
  });
});
