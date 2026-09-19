import React, { useState, useEffect, useCallback, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  Trash2,
  ArrowUp,
  ArrowDown,
  ShieldAlert,
} from "lucide-react";
import {
  Button,
  IconButton,
  Input,
  Select,
  Switch,
  Alert,
  Badge,
  Dialog,
} from "../../../shared/ui";
import { formatTimestamp } from "../../../shared/formatters";
import { AdminKeyDetail } from "../types";
import { updateKeyPolicy } from "../api";
import { keyQueryKeys } from "../queryKeys";
import {
  KeyPolicyFormValues,
  PolicyValidationErrors,
  SecuritySensitiveChange,
  DurationUnit,
  BudgetUnit,
  policyToFormValues,
  validatePolicyForm,
  formValuesToPolicyPayload,
  detectSecuritySensitiveChanges,
  reorderItem,
  generateRowId,
  dollarsStringToMicros,
} from "../policyHelpers";

export interface KeyPolicyFormProps {
  keyDetail: AdminKeyDetail;
  onSuccess?: (updated: AdminKeyDetail) => void;
  onCancel?: () => void;
  onDirtyChange?: (isDirty: boolean) => void;
  readOnly?: boolean;
}

export const KeyPolicyForm: React.FC<KeyPolicyFormProps> = ({
  keyDetail,
  onSuccess,
  onCancel,
  onDirtyChange,
  readOnly = false,
}) => {
  const queryClient = useQueryClient();

  // Baseline data loaded from server
  const [initialUpdatedAt, setInitialUpdatedAt] = useState<string>(keyDetail.updated_at);
  const [initialSerialized, setInitialSerialized] = useState<string>(() =>
    JSON.stringify(formValuesToPolicyPayload(policyToFormValues(keyDetail)))
  );

  // Active form state
  const [values, setValues] = useState<KeyPolicyFormValues>(() =>
    policyToFormValues(keyDetail)
  );

  // Validation, errors, and state
  const [validationErrors, setValidationErrors] = useState<PolicyValidationErrors>({
    summary: [],
    fieldErrors: {},
  });
  const [sensitiveChanges, setSensitiveChanges] = useState<SecuritySensitiveChange[]>([]);
  const [isReviewDialogOpen, setIsReviewDialogOpen] = useState<boolean>(false);
  const [isDiscardDialogOpen, setIsDiscardDialogOpen] = useState<boolean>(false);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [is409Conflict, setIs409Conflict] = useState<boolean>(false);
  const [serverConflict, setServerConflict] = useState<boolean>(false);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  // Compute dirty status
  const currentSerialized = useMemo(() => {
    try {
      return JSON.stringify(formValuesToPolicyPayload(values));
    } catch {
      return "";
    }
  }, [values]);

  const isDirty = useMemo(() => {
    return currentSerialized !== initialSerialized;
  }, [currentSerialized, initialSerialized]);

  // Report dirty status change
  useEffect(() => {
    onDirtyChange?.(isDirty);
  }, [isDirty, onDirtyChange]);

  // Warn before browser unload if dirty
  useEffect(() => {
    if (!isDirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [isDirty]);

  // Detect concurrent background modifications from query refetch
  useEffect(() => {
    if (isDirty && keyDetail.updated_at !== initialUpdatedAt) {
      setServerConflict(true);
    }
  }, [keyDetail.updated_at, initialUpdatedAt, isDirty]);

  // Reset form to latest server record
  const resetToServer = useCallback(
    (detail: AdminKeyDetail) => {
      const freshValues = policyToFormValues(detail);
      setValues(freshValues);
      setInitialUpdatedAt(detail.updated_at);
      setInitialSerialized(JSON.stringify(formValuesToPolicyPayload(freshValues)));
      setValidationErrors({ summary: [], fieldErrors: {} });
      setSubmitError(null);
      setIs409Conflict(false);
      setServerConflict(false);
      setSuccessMessage(null);
    },
    []
  );

  // Field change handlers
  const handleEnabledChange = (checked: boolean) => {
    setValues((prev) => ({ ...prev, enabled: checked }));
  };

  // Allowed models
  const handleAddAllowedModel = () => {
    setValues((prev) => ({
      ...prev,
      allowed_models: [...prev.allowed_models, ""],
    }));
  };

  const handleUpdateAllowedModel = (index: number, pattern: string) => {
    setValues((prev) => {
      const copy = [...prev.allowed_models];
      copy[index] = pattern;
      return { ...prev, allowed_models: copy };
    });
  };

  const handleRemoveAllowedModel = (index: number) => {
    setValues((prev) => ({
      ...prev,
      allowed_models: prev.allowed_models.filter((_, i) => i !== index),
    }));
  };

  const handleReorderAllowedModel = (index: number, direction: "up" | "down") => {
    setValues((prev) => ({
      ...prev,
      allowed_models: reorderItem(prev.allowed_models, index, direction),
    }));
  };

  // Denied models
  const handleAddDeniedModel = () => {
    setValues((prev) => ({
      ...prev,
      denied_models: [...prev.denied_models, ""],
    }));
  };

  const handleUpdateDeniedModel = (index: number, pattern: string) => {
    setValues((prev) => {
      const copy = [...prev.denied_models];
      copy[index] = pattern;
      return { ...prev, denied_models: copy };
    });
  };

  const handleRemoveDeniedModel = (index: number) => {
    setValues((prev) => ({
      ...prev,
      denied_models: prev.denied_models.filter((_, i) => i !== index),
    }));
  };

  const handleReorderDeniedModel = (index: number, direction: "up" | "down") => {
    setValues((prev) => ({
      ...prev,
      denied_models: reorderItem(prev.denied_models, index, direction),
    }));
  };

  // Request windows
  const handleAddRequestWindow = () => {
    setValues((prev) => ({
      ...prev,
      request_windows: [
        ...prev.request_windows,
        {
          id: generateRowId(),
          amount: 60,
          durationValue: 1,
          durationUnit: "m",
        },
      ],
    }));
  };

  const handleUpdateRequestWindow = (
    index: number,
    partial: { amount?: number | ""; durationValue?: number | ""; durationUnit?: DurationUnit }
  ) => {
    setValues((prev) => {
      const copy = [...prev.request_windows];
      const item = copy[index];
      if (item) {
        copy[index] = { ...item, ...partial };
      }
      return { ...prev, request_windows: copy };
    });
  };

  const handleRemoveRequestWindow = (index: number) => {
    setValues((prev) => ({
      ...prev,
      request_windows: prev.request_windows.filter((_, i) => i !== index),
    }));
  };

  const handleReorderRequestWindow = (index: number, direction: "up" | "down") => {
    setValues((prev) => ({
      ...prev,
      request_windows: reorderItem(prev.request_windows, index, direction),
    }));
  };

  // Token windows
  const handleAddTokenWindow = () => {
    setValues((prev) => ({
      ...prev,
      token_windows: [
        ...prev.token_windows,
        {
          id: generateRowId(),
          amount: 100000,
          durationValue: 1,
          durationUnit: "h",
        },
      ],
    }));
  };

  const handleUpdateTokenWindow = (
    index: number,
    partial: { amount?: number | ""; durationValue?: number | ""; durationUnit?: DurationUnit }
  ) => {
    setValues((prev) => {
      const copy = [...prev.token_windows];
      const item = copy[index];
      if (item) {
        copy[index] = { ...item, ...partial };
      }
      return { ...prev, token_windows: copy };
    });
  };

  const handleRemoveTokenWindow = (index: number) => {
    setValues((prev) => ({
      ...prev,
      token_windows: prev.token_windows.filter((_, i) => i !== index),
    }));
  };

  const handleReorderTokenWindow = (index: number, direction: "up" | "down") => {
    setValues((prev) => ({
      ...prev,
      token_windows: reorderItem(prev.token_windows, index, direction),
    }));
  };

  // Concurrency
  const handleConcurrencyModeChange = (mode: "unlimited" | "custom") => {
    setValues((prev) => ({
      ...prev,
      concurrency_mode: mode,
      max_concurrent_requests: mode === "custom" ? (prev.max_concurrent_requests || 5) : "",
    }));
  };

  const handleConcurrencyValueChange = (val: number | "") => {
    setValues((prev) => ({
      ...prev,
      max_concurrent_requests: val,
    }));
  };

  // Token Mode
  const handleTokenModeChange = (mode: string) => {
    setValues((prev) => ({
      ...prev,
      token_mode: mode,
    }));
  };

  // Budget limits
  const handleAddBudgetLimit = () => {
    const usedPeriods = new Set(values.budget_limits.map((b) => b.period));
    const periods: ("total" | "day" | "month")[] = ["total", "day", "month"];
    const nextPeriod = periods.find((p) => !usedPeriods.has(p)) || "total";

    setValues((prev) => ({
      ...prev,
      budget_limits: [
        ...prev.budget_limits,
        {
          id: generateRowId(),
          period: nextPeriod,
          amount: "10.00",
          unit: "usd",
        },
      ],
    }));
  };

  const handleUpdateBudgetLimit = (
    index: number,
    partial: { period?: "total" | "day" | "month"; amount?: string; unit?: BudgetUnit }
  ) => {
    setValues((prev) => {
      const copy = [...prev.budget_limits];
      const item = copy[index];
      if (item) {
        copy[index] = { ...item, ...partial };
      }
      return { ...prev, budget_limits: copy };
    });
  };

  const handleRemoveBudgetLimit = (index: number) => {
    setValues((prev) => ({
      ...prev,
      budget_limits: prev.budget_limits.filter((_, i) => i !== index),
    }));
  };

  const handleReorderBudgetLimit = (index: number, direction: "up" | "down") => {
    setValues((prev) => ({
      ...prev,
      budget_limits: reorderItem(prev.budget_limits, index, direction),
    }));
  };

  // Logging switches
  const handleLogRequestBodyChange = (checked: boolean) => {
    setValues((prev) => ({ ...prev, log_request_body: checked }));
  };

  const handleLogResponseBodyChange = (checked: boolean) => {
    setValues((prev) => ({ ...prev, log_response_body: checked }));
  };

  // Submission logic
  const executeSubmit = async () => {
    setIsSubmitting(true);
    setSubmitError(null);
    setIs409Conflict(false);
    setSuccessMessage(null);
    setIsReviewDialogOpen(false);

    try {
      const payload = formValuesToPolicyPayload(values);
      const updated = await updateKeyPolicy(keyDetail.id, payload);

      // Server response is the source of truth
      queryClient.setQueryData(keyQueryKeys.detail(keyDetail.id), updated);
      queryClient.invalidateQueries({ queryKey: keyQueryKeys.list() });

      // Update baseline state to match updated server response
      const freshValues = policyToFormValues(updated);
      setValues(freshValues);
      setInitialUpdatedAt(updated.updated_at);
      setInitialSerialized(JSON.stringify(formValuesToPolicyPayload(freshValues)));
      setSuccessMessage("Key policy and status replaced successfully.");
      onSuccess?.(updated);
    } catch (err: unknown) {
      const isConflict =
        err &&
        typeof err === "object" &&
        ("status" in err ? err.status === 409 : "code" in err && err.code === "conflict");

      if (isConflict) {
        setIs409Conflict(true);
        setSubmitError(
          "Active token usage prevents this policy change right now. Your edits have been preserved. You can safely retry."
        );
      } else {
        const msg =
          err instanceof Error
            ? err.message
            : typeof err === "object" && err !== null && "message" in err
            ? String((err as { message: unknown }).message)
            : "Failed to update key policy.";
        setSubmitError(msg);
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleFormSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitError(null);
    setIs409Conflict(false);
    setSuccessMessage(null);

    // Validate fields
    const errors = validatePolicyForm(values);
    if (errors.summary.length > 0) {
      setValidationErrors(errors);
      // Focus first errored field
      if (errors.firstErrorFieldId) {
        const el = document.getElementById(errors.firstErrorFieldId);
        if (el) {
          el.focus();
          if (typeof el.scrollIntoView === "function") {
            el.scrollIntoView({ behavior: "smooth", block: "center" });
          }
        }
      }
      return;
    }

    setValidationErrors({ summary: [], fieldErrors: {} });

    // Detect security sensitive changes
    const sensitive = detectSecuritySensitiveChanges(keyDetail, values);
    if (sensitive.length > 0) {
      setSensitiveChanges(sensitive);
      setIsReviewDialogOpen(true);
      return;
    }

    // No security-sensitive items, submit directly
    void executeSubmit();
  };

  const handleCancelClick = () => {
    if (isDirty) {
      setIsDiscardDialogOpen(true);
    } else {
      onCancel?.();
    }
  };

  const handleConfirmDiscard = () => {
    setIsDiscardDialogOpen(false);
    resetToServer(keyDetail);
    onCancel?.();
  };

  return (
    <form
      className="gw-policy-form"
      onSubmit={handleFormSubmit}
      data-testid="key-policy-form"
      noValidate
    >
      {/* Success Notification Banner */}
      {successMessage && (
        <Alert
          variant="success"
          title="Policy Saved"
          onClose={() => setSuccessMessage(null)}
          data-testid="policy-success-alert"
        >
          {successMessage}
        </Alert>
      )}

      {/* Concurrent Server Update Warning Banner */}
      {serverConflict && (
        <Alert
          variant="warning"
          title="Concurrent Modification Detected"
          action={
            <div style={{ display: "flex", gap: "var(--space-2)" }}>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => setServerConflict(false)}
                data-testid="keep-local-edits-btn"
              >
                Keep My Edits
              </Button>
              <Button
                size="sm"
                variant="primary"
                onClick={() => resetToServer(keyDetail)}
                data-testid="reload-server-policy-btn"
              >
                Reload Server Policy
              </Button>
            </div>
          }
          data-testid="server-conflict-alert"
        >
          This key was modified on the server at {formatTimestamp(keyDetail.updated_at)} while you had unsaved edits.
          Choose whether to keep your local changes or reload the server state.
        </Alert>
      )}

      {/* 409 Conflict Banner with Safe Retry Path */}
      {is409Conflict && (
        <Alert
          variant="warning"
          title="Policy Replacement Conflict (HTTP 409)"
          action={
            <Button
              size="sm"
              variant="primary"
              onClick={() => void executeSubmit()}
              isLoading={isSubmitting}
              data-testid="conflict-retry-btn"
            >
              Retry Save
            </Button>
          }
          data-testid="conflict-409-alert"
        >
          {submitError || "Active token usage prevents this policy change right now. Your unsaved edits have been preserved."}
        </Alert>
      )}

      {/* General Error Banner */}
      {submitError && !is409Conflict && (
        <Alert
          variant="danger"
          title="Save Failed"
          action={
            <Button
              size="sm"
              variant="secondary"
              onClick={() => void executeSubmit()}
              isLoading={isSubmitting}
              data-testid="general-retry-btn"
            >
              Retry
            </Button>
          }
          data-testid="policy-submit-error-alert"
        >
          {submitError}
        </Alert>
      )}

      {/* Validation Summary Banner */}
      {validationErrors.summary.length > 0 && (
        <Alert
          variant="danger"
          title="Please correct the following errors before saving:"
          data-testid="policy-validation-summary"
        >
          <ul style={{ margin: 0, paddingLeft: "var(--space-4)" }}>
            {validationErrors.summary.map((err, i) => (
              <li key={i}>{err}</li>
            ))}
          </ul>
        </Alert>
      )}

      {/* Section 1: Key Enabled Status */}
      <section className="gw-policy-section" aria-labelledby="policy-enabled-title">
        <h4 id="policy-enabled-title" className="gw-policy-section-title">
          Key Access Status
        </h4>
        <div className="gw-policy-section-content">
          <Switch
            id="policy-enabled-switch"
            label={values.enabled ? "API Key is Enabled" : "API Key is Disabled"}
            description={
              values.enabled
                ? "Incoming requests presenting this key will be authenticated and evaluated against limits."
                : "When disabled, incoming requests with this key are rejected immediately with HTTP 401. Key secrets and usage history are preserved."
            }
            checked={values.enabled}
            onChange={handleEnabledChange}
            disabled={readOnly || isSubmitting}
            data-testid="policy-enabled-switch"
          />
        </div>
      </section>

      {/* Section 2: Allowed Models */}
      <section className="gw-policy-section" aria-labelledby="policy-allowed-models-title">
        <div className="gw-policy-section-header">
          <div>
            <h4 id="policy-allowed-models-title" className="gw-policy-section-title">
              Allowed Models (Allowlist)
            </h4>
            <p className="gw-policy-section-hint">
              Glob patterns for permitted models (e.g. <code>gpt-4*</code>, <code>claude-3-5-sonnet</code>). If empty, all models are permitted (unless denied).
            </p>
          </div>
          {!readOnly && (
            <Button
              size="sm"
              variant="outline"
              leftIcon={<Plus size={14} aria-hidden="true" />}
              onClick={handleAddAllowedModel}
              disabled={isSubmitting}
              data-testid="add-allowed-model-btn"
            >
              Add Pattern
            </Button>
          )}
        </div>

        {values.allowed_models.length === 0 ? (
          <div className="gw-policy-empty-notice" data-testid="allowed-models-empty">
            <Badge variant="neutral" size="sm">
              All models permitted (unrestricted allowlist)
            </Badge>
          </div>
        ) : (
          <div className="gw-policy-list">
            {values.allowed_models.map((pattern, index) => {
              const fieldId = `allowed-model-${index}`;
              const error = validationErrors.fieldErrors[fieldId];
              return (
                <div key={index} className="gw-policy-list-row" data-testid={`allowed-model-row-${index}`}>
                  <div style={{ flex: 1 }}>
                    <Input
                      id={fieldId}
                      value={pattern}
                      placeholder="e.g. gpt-4* or exact-model-id"
                      onChange={(e) => handleUpdateAllowedModel(index, e.target.value)}
                      error={error}
                      disabled={readOnly || isSubmitting}
                      data-testid={`allowed-model-input-${index}`}
                    />
                  </div>
                  {!readOnly && (
                    <div className="gw-policy-row-actions">
                      <IconButton
                        aria-label={`Move allowed model ${pattern || index + 1} up`}
                        icon={<ArrowUp size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === 0 || isSubmitting}
                        onClick={() => handleReorderAllowedModel(index, "up")}
                        data-testid={`allowed-model-up-${index}`}
                      />
                      <IconButton
                        aria-label={`Move allowed model ${pattern || index + 1} down`}
                        icon={<ArrowDown size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === values.allowed_models.length - 1 || isSubmitting}
                        onClick={() => handleReorderAllowedModel(index, "down")}
                        data-testid={`allowed-model-down-${index}`}
                      />
                      <IconButton
                        aria-label={`Remove allowed model ${pattern || index + 1}`}
                        icon={<Trash2 size={14} aria-hidden="true" />}
                        size="sm"
                        variant="danger"
                        disabled={isSubmitting}
                        onClick={() => handleRemoveAllowedModel(index)}
                        data-testid={`allowed-model-remove-${index}`}
                      />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>

      {/* Section 3: Denied Models */}
      <section className="gw-policy-section" aria-labelledby="policy-denied-models-title">
        <div className="gw-policy-section-header">
          <div>
            <h4 id="policy-denied-models-title" className="gw-policy-section-title">
              Denied Models (Denylist)
            </h4>
            <p className="gw-policy-section-hint">
              <strong>Precedence:</strong> Deny rules take absolute precedence over allow rules. If a model matches any denied pattern, it is rejected immediately.
            </p>
          </div>
          {!readOnly && (
            <Button
              size="sm"
              variant="outline"
              leftIcon={<Plus size={14} aria-hidden="true" />}
              onClick={handleAddDeniedModel}
              disabled={isSubmitting}
              data-testid="add-denied-model-btn"
            >
              Add Pattern
            </Button>
          )}
        </div>

        {values.denied_models.length === 0 ? (
          <div className="gw-policy-empty-notice" data-testid="denied-models-empty">
            <span style={{ color: "var(--text-secondary)", fontSize: "var(--font-size-sm)" }}>
              No models denied
            </span>
          </div>
        ) : (
          <div className="gw-policy-list">
            {values.denied_models.map((pattern, index) => {
              const fieldId = `denied-model-${index}`;
              const error = validationErrors.fieldErrors[fieldId];
              return (
                <div key={index} className="gw-policy-list-row" data-testid={`denied-model-row-${index}`}>
                  <div style={{ flex: 1 }}>
                    <Input
                      id={fieldId}
                      value={pattern}
                      placeholder="e.g. gpt-4-internal or *-secret"
                      onChange={(e) => handleUpdateDeniedModel(index, e.target.value)}
                      error={error}
                      disabled={readOnly || isSubmitting}
                      data-testid={`denied-model-input-${index}`}
                    />
                  </div>
                  {!readOnly && (
                    <div className="gw-policy-row-actions">
                      <IconButton
                        aria-label={`Move denied model ${pattern || index + 1} up`}
                        icon={<ArrowUp size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === 0 || isSubmitting}
                        onClick={() => handleReorderDeniedModel(index, "up")}
                        data-testid={`denied-model-up-${index}`}
                      />
                      <IconButton
                        aria-label={`Move denied model ${pattern || index + 1} down`}
                        icon={<ArrowDown size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === values.denied_models.length - 1 || isSubmitting}
                        onClick={() => handleReorderDeniedModel(index, "down")}
                        data-testid={`denied-model-down-${index}`}
                      />
                      <IconButton
                        aria-label={`Remove denied model ${pattern || index + 1}`}
                        icon={<Trash2 size={14} aria-hidden="true" />}
                        size="sm"
                        variant="danger"
                        disabled={isSubmitting}
                        onClick={() => handleRemoveDeniedModel(index)}
                        data-testid={`denied-model-remove-${index}`}
                      />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>

      {/* Section 4: Request Rate Limits */}
      <section className="gw-policy-section" aria-labelledby="policy-req-limits-title">
        <div className="gw-policy-section-header">
          <div>
            <h4 id="policy-req-limits-title" className="gw-policy-section-title">
              Request Rate Limits
            </h4>
            <p className="gw-policy-section-hint">
              Enforce request frequency over fixed time windows aligned to integer multiples from Unix epoch. Empty list indicates unlimited requests.
            </p>
          </div>
          {!readOnly && (
            <Button
              size="sm"
              variant="outline"
              leftIcon={<Plus size={14} aria-hidden="true" />}
              onClick={handleAddRequestWindow}
              disabled={isSubmitting}
              data-testid="add-req-window-btn"
            >
              Add Limit
            </Button>
          )}
        </div>

        {values.request_windows.length === 0 ? (
          <div className="gw-policy-empty-notice" data-testid="req-windows-empty">
            <Badge variant="neutral" size="sm">
              Unlimited request rate
            </Badge>
          </div>
        ) : (
          <div className="gw-policy-list">
            {values.request_windows.map((w, index) => {
              const amountField = `req-window-amount-${index}`;
              const durationField = `req-window-duration-${index}`;
              const amountError = validationErrors.fieldErrors[amountField];
              const durationError = validationErrors.fieldErrors[durationField];

              return (
                <div key={w.id} className="gw-policy-window-row" data-testid={`req-window-row-${index}`}>
                  <div className="gw-policy-window-inputs">
                    <div style={{ width: "130px" }}>
                      <Input
                        id={amountField}
                        type="number"
                        min="1"
                        step="1"
                        label="Requests"
                        value={w.amount}
                        placeholder="e.g. 60"
                        error={amountError}
                        onChange={(e) =>
                          handleUpdateRequestWindow(index, {
                            amount: e.target.value === "" ? "" : parseInt(e.target.value, 10),
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`req-amount-${index}`}
                      />
                    </div>
                    <span className="gw-policy-inline-label">per</span>
                    <div style={{ width: "100px" }}>
                      <Input
                        id={durationField}
                        type="number"
                        min="1"
                        step="1"
                        label="Duration"
                        value={w.durationValue}
                        placeholder="e.g. 1"
                        error={durationError}
                        onChange={(e) =>
                          handleUpdateRequestWindow(index, {
                            durationValue: e.target.value === "" ? "" : parseInt(e.target.value, 10),
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`req-duration-val-${index}`}
                      />
                    </div>
                    <div style={{ width: "130px" }}>
                      <Select
                        label="Unit"
                        value={w.durationUnit}
                        onChange={(e) =>
                          handleUpdateRequestWindow(index, {
                            durationUnit: e.target.value as DurationUnit,
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`req-duration-unit-${index}`}
                        options={[
                          { value: "s", label: "Seconds" },
                          { value: "m", label: "Minutes" },
                          { value: "h", label: "Hours" },
                          { value: "d", label: "Days" },
                        ]}
                      />
                    </div>
                  </div>

                  {!readOnly && (
                    <div className="gw-policy-row-actions">
                      <IconButton
                        aria-label={`Move request window ${index + 1} up`}
                        icon={<ArrowUp size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === 0 || isSubmitting}
                        onClick={() => handleReorderRequestWindow(index, "up")}
                        data-testid={`req-window-up-${index}`}
                      />
                      <IconButton
                        aria-label={`Move request window ${index + 1} down`}
                        icon={<ArrowDown size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === values.request_windows.length - 1 || isSubmitting}
                        onClick={() => handleReorderRequestWindow(index, "down")}
                        data-testid={`req-window-down-${index}`}
                      />
                      <IconButton
                        aria-label={`Remove request window ${index + 1}`}
                        icon={<Trash2 size={14} aria-hidden="true" />}
                        size="sm"
                        variant="danger"
                        disabled={isSubmitting}
                        onClick={() => handleRemoveRequestWindow(index)}
                        data-testid={`req-window-remove-${index}`}
                      />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>

      {/* Section 5: Token Rate Limits & Mode */}
      <section className="gw-policy-section" aria-labelledby="policy-token-limits-title">
        <div className="gw-policy-section-header">
          <div>
            <h4 id="policy-token-limits-title" className="gw-policy-section-title">
              Token Rate Limits
            </h4>
            <p className="gw-policy-section-hint">
              Enforce cumulative token budgets across fixed time windows. Empty list indicates unlimited tokens.
            </p>
          </div>
          {!readOnly && (
            <Button
              size="sm"
              variant="outline"
              leftIcon={<Plus size={14} aria-hidden="true" />}
              onClick={handleAddTokenWindow}
              disabled={isSubmitting}
              data-testid="add-token-window-btn"
            >
              Add Limit
            </Button>
          )}
        </div>

        {values.token_windows.length === 0 ? (
          <div className="gw-policy-empty-notice" data-testid="token-windows-empty">
            <Badge variant="neutral" size="sm">
              Unlimited token rate
            </Badge>
          </div>
        ) : (
          <div className="gw-policy-list">
            {values.token_windows.map((w, index) => {
              const amountField = `tok-window-amount-${index}`;
              const durationField = `tok-window-duration-${index}`;
              const amountError = validationErrors.fieldErrors[amountField];
              const durationError = validationErrors.fieldErrors[durationField];

              return (
                <div key={w.id} className="gw-policy-window-row" data-testid={`tok-window-row-${index}`}>
                  <div className="gw-policy-window-inputs">
                    <div style={{ width: "150px" }}>
                      <Input
                        id={amountField}
                        type="number"
                        min="1"
                        step="1"
                        label="Tokens"
                        value={w.amount}
                        placeholder="e.g. 100000"
                        error={amountError}
                        onChange={(e) =>
                          handleUpdateTokenWindow(index, {
                            amount: e.target.value === "" ? "" : parseInt(e.target.value, 10),
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`tok-amount-${index}`}
                      />
                    </div>
                    <span className="gw-policy-inline-label">per</span>
                    <div style={{ width: "100px" }}>
                      <Input
                        id={durationField}
                        type="number"
                        min="1"
                        step="1"
                        label="Duration"
                        value={w.durationValue}
                        placeholder="e.g. 1"
                        error={durationError}
                        onChange={(e) =>
                          handleUpdateTokenWindow(index, {
                            durationValue: e.target.value === "" ? "" : parseInt(e.target.value, 10),
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`tok-duration-val-${index}`}
                      />
                    </div>
                    <div style={{ width: "130px" }}>
                      <Select
                        label="Unit"
                        value={w.durationUnit}
                        onChange={(e) =>
                          handleUpdateTokenWindow(index, {
                            durationUnit: e.target.value as DurationUnit,
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`tok-duration-unit-${index}`}
                        options={[
                          { value: "s", label: "Seconds" },
                          { value: "m", label: "Minutes" },
                          { value: "h", label: "Hours" },
                          { value: "d", label: "Days" },
                        ]}
                      />
                    </div>
                  </div>

                  {!readOnly && (
                    <div className="gw-policy-row-actions">
                      <IconButton
                        aria-label={`Move token window ${index + 1} up`}
                        icon={<ArrowUp size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === 0 || isSubmitting}
                        onClick={() => handleReorderTokenWindow(index, "up")}
                        data-testid={`tok-window-up-${index}`}
                      />
                      <IconButton
                        aria-label={`Move token window ${index + 1} down`}
                        icon={<ArrowDown size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === values.token_windows.length - 1 || isSubmitting}
                        onClick={() => handleReorderTokenWindow(index, "down")}
                        data-testid={`tok-window-down-${index}`}
                      />
                      <IconButton
                        aria-label={`Remove token window ${index + 1}`}
                        icon={<Trash2 size={14} aria-hidden="true" />}
                        size="sm"
                        variant="danger"
                        disabled={isSubmitting}
                        onClick={() => handleRemoveTokenWindow(index)}
                        data-testid={`tok-window-remove-${index}`}
                      />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {/* Token Accounting Mode */}
        <div style={{ marginTop: "var(--space-3)", maxWidth: "420px" }}>
          <Select
            id="policy-token-mode-select"
            label="Token Accounting Mode"
            helperText="Estimate reserves tokens prior to upstream forward and reconciles upon completion. Usage only counts verified upstream tokens."
            value={values.token_mode}
            onChange={(e) => handleTokenModeChange(e.target.value)}
            disabled={readOnly || isSubmitting}
            data-testid="token-mode-select"
            options={[
              { value: "estimate", label: "Estimate & Reconcile (pre-request reservation)" },
              { value: "usage_only", label: "Usage Only (account strictly on completion)" },
            ]}
          />
        </div>
      </section>

      {/* Section 6: Concurrency */}
      <section className="gw-policy-section" aria-labelledby="policy-concurrency-title">
        <h4 id="policy-concurrency-title" className="gw-policy-section-title">
          Concurrency Limit
        </h4>
        <p className="gw-policy-section-hint">
          Limits simultaneous in-flight requests processed by this key. When saturated, additional requests reject immediately with HTTP 429 without queueing.
        </p>

        <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)", maxWidth: "340px" }}>
          <div style={{ display: "flex", gap: "var(--space-4)", alignItems: "center" }}>
            <label style={{ display: "flex", alignItems: "center", gap: "var(--space-2)", cursor: "pointer" }}>
              <input
                type="radio"
                name="concurrency-mode"
                checked={values.concurrency_mode === "unlimited"}
                onChange={() => handleConcurrencyModeChange("unlimited")}
                disabled={readOnly || isSubmitting}
                data-testid="concurrency-unlimited-radio"
              />
              <span>Unlimited Concurrency</span>
            </label>
            <label style={{ display: "flex", alignItems: "center", gap: "var(--space-2)", cursor: "pointer" }}>
              <input
                type="radio"
                name="concurrency-mode"
                checked={values.concurrency_mode === "custom"}
                onChange={() => handleConcurrencyModeChange("custom")}
                disabled={readOnly || isSubmitting}
                data-testid="concurrency-custom-radio"
              />
              <span>Set Limit</span>
            </label>
          </div>

          {values.concurrency_mode === "custom" && (
            <Input
              id="max-concurrency-input"
              type="number"
              min="1"
              step="1"
              label="Maximum In-Flight Requests"
              value={values.max_concurrent_requests}
              placeholder="e.g. 5"
              error={validationErrors.fieldErrors["max-concurrency-input"]}
              onChange={(e) =>
                handleConcurrencyValueChange(
                  e.target.value === "" ? "" : parseInt(e.target.value, 10)
                )
              }
              disabled={readOnly || isSubmitting}
              data-testid="max-concurrency-input"
            />
          )}
        </div>
      </section>

      {/* Section 7: Budget Limits */}
      <section className="gw-policy-section" aria-labelledby="policy-budgets-title">
        <div className="gw-policy-section-header">
          <div>
            <h4 id="policy-budgets-title" className="gw-policy-section-title">
              Budget Limits
            </h4>
            <p className="gw-policy-section-hint">
              <strong>Semantics:</strong> Restricts cumulative expenditure. Total limits lifetime spend; Day and Month reset at UTC calendar boundaries. Historical spend is preserved across policy edits. Reaching any limit blocks further requests with HTTP 429.
            </p>
          </div>
          {!readOnly && values.budget_limits.length < 3 && (
            <Button
              size="sm"
              variant="outline"
              leftIcon={<Plus size={14} aria-hidden="true" />}
              onClick={handleAddBudgetLimit}
              disabled={isSubmitting}
              data-testid="add-budget-limit-btn"
            >
              Add Budget
            </Button>
          )}
        </div>

        {values.budget_limits.length === 0 ? (
          <div className="gw-policy-empty-notice" data-testid="budgets-empty">
            <span style={{ color: "var(--text-secondary)", fontSize: "var(--font-size-sm)" }}>
              No budget limits configured (unrestricted expenditure)
            </span>
          </div>
        ) : (
          <div className="gw-policy-list">
            {values.budget_limits.map((b, index) => {
              const periodField = `budget-period-${index}`;
              const amountField = `budget-amount-${index}`;
              const periodError = validationErrors.fieldErrors[periodField];
              const amountError = validationErrors.fieldErrors[amountField];

              // Display equivalent conversion helper
              let helperText = "";
              if (b.unit === "usd") {
                const micros = dollarsStringToMicros(b.amount);
                if (micros !== null && micros > 0) {
                  helperText = `Equals ${micros.toLocaleString()} µ$`;
                }
              } else {
                const num = parseInt(b.amount.trim(), 10);
                if (!isNaN(num) && num > 0) {
                  helperText = `Equals $${(num / 1_000_000).toFixed(6)} USD`;
                }
              }

              return (
                <div key={b.id} className="gw-policy-budget-row" data-testid={`budget-row-${index}`}>
                  <div className="gw-policy-budget-inputs">
                    <div style={{ width: "160px" }}>
                      <Select
                        id={periodField}
                        label="Period"
                        value={b.period}
                        error={periodError}
                        onChange={(e) =>
                          handleUpdateBudgetLimit(index, {
                            period: e.target.value as "total" | "day" | "month",
                          })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`budget-period-${index}`}
                        options={[
                          { value: "total", label: "Lifetime Total" },
                          { value: "day", label: "Daily (UTC)" },
                          { value: "month", label: "Monthly (UTC)" },
                        ]}
                      />
                    </div>

                    <div style={{ width: "160px" }}>
                      <Input
                        id={amountField}
                        type="text"
                        label="Limit Amount"
                        value={b.amount}
                        placeholder={b.unit === "usd" ? "10.00" : "1000000"}
                        error={amountError}
                        helperText={helperText}
                        onChange={(e) =>
                          handleUpdateBudgetLimit(index, { amount: e.target.value })
                        }
                        disabled={readOnly || isSubmitting}
                        data-testid={`budget-amount-${index}`}
                      />
                    </div>

                    <div style={{ width: "150px" }}>
                      <Select
                        label="Currency Unit"
                        value={b.unit}
                        onChange={(e) => {
                          const newUnit = e.target.value as BudgetUnit;
                          // Convert between units losslessly if valid
                          let nextAmount = b.amount;
                          if (newUnit === "micros" && b.unit === "usd") {
                            const m = dollarsStringToMicros(b.amount);
                            if (m !== null) nextAmount = String(m);
                          } else if (newUnit === "usd" && b.unit === "micros") {
                            const n = parseInt(b.amount.trim(), 10);
                            if (!isNaN(n)) nextAmount = String(n / 1_000_000);
                          }
                          handleUpdateBudgetLimit(index, { unit: newUnit, amount: nextAmount });
                        }}
                        disabled={readOnly || isSubmitting}
                        data-testid={`budget-unit-${index}`}
                        options={[
                          { value: "usd", label: "USD ($)" },
                          { value: "micros", label: "µ$ (micros)" },
                        ]}
                      />
                    </div>
                  </div>

                  {!readOnly && (
                    <div className="gw-policy-row-actions">
                      <IconButton
                        aria-label={`Move budget limit ${index + 1} up`}
                        icon={<ArrowUp size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === 0 || isSubmitting}
                        onClick={() => handleReorderBudgetLimit(index, "up")}
                        data-testid={`budget-up-${index}`}
                      />
                      <IconButton
                        aria-label={`Move budget limit ${index + 1} down`}
                        icon={<ArrowDown size={14} aria-hidden="true" />}
                        size="sm"
                        disabled={index === values.budget_limits.length - 1 || isSubmitting}
                        onClick={() => handleReorderBudgetLimit(index, "down")}
                        data-testid={`budget-down-${index}`}
                      />
                      <IconButton
                        aria-label={`Remove budget limit ${index + 1}`}
                        icon={<Trash2 size={14} aria-hidden="true" />}
                        size="sm"
                        variant="danger"
                        disabled={isSubmitting}
                        onClick={() => handleRemoveBudgetLimit(index)}
                        data-testid={`budget-remove-${index}`}
                      />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>

      {/* Section 8: Body Capture Logging Policy */}
      <section className="gw-policy-section" aria-labelledby="policy-logging-title">
        <h4 id="policy-logging-title" className="gw-policy-section-title">
          Body Capture Logging Policy
        </h4>
        <Alert variant="warning" title="Security & Data Privacy Sensitivity">
          Enabling body logging stores unredacted payload content in SQLite for debugging and auditing. Requests may contain proprietary prompts, PII, or API secrets. Response bodies may contain sensitive outputs.
        </Alert>

        <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)", marginTop: "var(--space-3)" }}>
          <Switch
            id="policy-log-req-switch"
            label="Log Client Request Bodies"
            description="Captures raw incoming request payload bodies (up to 1 MiB safety cap)."
            checked={values.log_request_body}
            onChange={handleLogRequestBodyChange}
            disabled={readOnly || isSubmitting}
            data-testid="log-request-body-switch"
          />

          <Switch
            id="policy-log-res-switch"
            label="Log Upstream Response Bodies"
            description="Captures raw model response bodies (up to 1 MiB safety cap)."
            checked={values.log_response_body}
            onChange={handleLogResponseBodyChange}
            disabled={readOnly || isSubmitting}
            data-testid="log-response-body-switch"
          />
        </div>
      </section>

      {/* Section 9: Read-Only Expiry Metadata */}
      <section className="gw-policy-section" aria-labelledby="policy-expiry-title">
        <h4 id="policy-expiry-title" className="gw-policy-section-title">
          Expiration Metadata
        </h4>
        <div className="gw-policy-meta-box">
          <div className="gw-policy-meta-item">
            <span className="gw-key-detail-label">Expiration Date</span>
            <span className="gw-key-detail-value" data-testid="policy-expiry-value">
              {keyDetail.expires_at ? formatTimestamp(keyDetail.expires_at) : "Never expires"}
            </span>
          </div>
          <p className="gw-key-detail-notice">
            API key expiration is immutable and cannot be updated after creation.
          </p>
        </div>
      </section>

      {/* Action Footer Bar */}
      {!readOnly && (
        <div className="gw-policy-actions-bar">
          <Button
            type="button"
            variant="secondary"
            onClick={handleCancelClick}
            disabled={isSubmitting}
            data-testid="cancel-policy-btn"
          >
            {isDirty ? "Discard Changes" : "Close"}
          </Button>

          <Button
            type="submit"
            variant="primary"
            isLoading={isSubmitting}
            disabled={!isDirty || isSubmitting}
            data-testid="save-policy-btn"
          >
            Save Policy Replacement
          </Button>
        </div>
      )}

      {/* Security Review Summary Dialog */}
      <Dialog
        isOpen={isReviewDialogOpen}
        onClose={() => setIsReviewDialogOpen(false)}
        title="Security-Sensitive Review Summary"
        description="Please review the following security-sensitive changes before replacing this key's policy:"
        footer={
          <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--space-2)", width: "100%" }}>
            <Button
              variant="secondary"
              onClick={() => setIsReviewDialogOpen(false)}
              disabled={isSubmitting}
              data-testid="review-dialog-cancel-btn"
            >
              Go Back
            </Button>
            <Button
              variant="primary"
              isLoading={isSubmitting}
              onClick={() => void executeSubmit()}
              data-testid="review-dialog-confirm-btn"
            >
              Confirm & Save Policy
            </Button>
          </div>
        }
      >
        <div className="gw-policy-review-list" data-testid="security-review-list">
          {sensitiveChanges.map((c) => (
            <div key={c.id} className={`gw-policy-review-item gw-policy-review-item--${c.severity}`}>
              <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
                <ShieldAlert
                  size={18}
                  color={c.severity === "high" ? "var(--color-danger)" : "var(--color-warning)"}
                  aria-hidden="true"
                />
                <strong style={{ fontSize: "var(--font-size-sm)" }}>{c.label}</strong>
              </div>
              <p style={{ margin: "4px 0 0 26px", fontSize: "var(--font-size-xs)", color: "var(--text-secondary)" }}>
                {c.description}
              </p>
            </div>
          ))}
        </div>
      </Dialog>

      {/* Discard Unsaved Changes Confirmation Dialog */}
      <Dialog
        isOpen={isDiscardDialogOpen}
        onClose={() => setIsDiscardDialogOpen(false)}
        title="Discard Unsaved Changes?"
        description="You have unsaved policy modifications. Leaving now will discard your edits."
        footer={
          <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--space-2)", width: "100%" }}>
            <Button
              variant="secondary"
              onClick={() => setIsDiscardDialogOpen(false)}
              data-testid="discard-dialog-cancel-btn"
            >
              Keep Editing
            </Button>
            <Button
              variant="danger"
              onClick={handleConfirmDiscard}
              data-testid="discard-dialog-confirm-btn"
            >
              Discard Changes
            </Button>
          </div>
        }
      >
        <p style={{ fontSize: "var(--font-size-sm)", color: "var(--text-secondary)", margin: 0 }}>
          Any unsaved adjustments to model patterns, rate windows, concurrency, budgets, or logging flags will be lost.
        </p>
      </Dialog>
    </form>
  );
};
