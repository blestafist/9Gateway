import React, { useState, useEffect, useRef, useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Copy, Check, Download, Plus } from "lucide-react";
import {
  Dialog,
  Input,
  Select,
  Checkbox,
  Button,
  IconButton,
  Alert,
} from "../../../shared/ui";
import { formatTimestamp } from "../../../shared/formatters";
import { useAuth } from "../../auth";
import { createKey } from "../api";
import { keyQueryKeys } from "../queryKeys";
import {
  generateKeyDownloadFilename,
  downloadKeySecret,
  parseCustomExpiryToMs,
  validateCustomExpiry,
} from "../helpers";

export { parseCustomExpiryToMs, validateCustomExpiry };

export interface CreateKeyDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess?: (created: { id: string; name: string; prefix: string }) => void;
}

export interface CreatedKeyMetadata {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  expires_at?: string;
}

export type ExpiryMode = "never" | "30d" | "60d" | "90d" | "1y" | "custom";

const EXPIRY_OPTIONS = [
  { value: "never", label: "Never (no expiration)" },
  { value: "30d", label: "30 days" },
  { value: "60d", label: "60 days" },
  { value: "90d", label: "90 days" },
  { value: "1y", label: "1 year" },
  { value: "custom", label: "Custom UTC date & time" },
];

export function computeExpiresAt(mode: ExpiryMode, customVal: string): string | undefined {
  if (mode === "never") {
    return undefined;
  }
  const now = Date.now();
  let targetMs: number;
  if (mode === "30d") {
    targetMs = now + 30 * 86400 * 1000;
  } else if (mode === "60d") {
    targetMs = now + 60 * 86400 * 1000;
  } else if (mode === "90d") {
    targetMs = now + 90 * 86400 * 1000;
  } else if (mode === "1y") {
    targetMs = now + 365 * 86400 * 1000;
  } else {
    targetMs = parseCustomExpiryToMs(customVal);
  }
  if (Number.isNaN(targetMs)) {
    return undefined;
  }
  const d = new Date(Math.floor(targetMs / 1000) * 1000);
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export const CreateKeyDialog: React.FC<CreateKeyDialogProps> = ({
  isOpen,
  onClose,
  onSuccess,
}) => {
  const queryClient = useQueryClient();
  const auth = useAuth();

  // Wizard step: form vs blocking success credential handoff
  const [step, setStep] = useState<"form" | "success">("form");

  // Form input state
  const [name, setName] = useState("");
  const [expiryMode, setExpiryMode] = useState<ExpiryMode>("never");
  const [customExpiry, setCustomExpiry] = useState("");

  // Validation & submission state
  const [nameError, setNameError] = useState<string | null>(null);
  const [expiryError, setExpiryError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const isSubmittingRef = useRef(false);

  // Success credential handoff state (HELD STRICTLY IN COMPONENT MEMORY)
  const [rawKey, setRawKey] = useState<string | null>(null);
  const [createdMetadata, setCreatedMetadata] = useState<CreatedKeyMetadata | null>(null);
  const [isSecretVisible, setIsSecretVisible] = useState(false);
  const [hasCopied, setHasCopied] = useState(false);
  const [copyError, setCopyError] = useState<string | null>(null);
  const [hasAcknowledged, setHasAcknowledged] = useState(false);
  const [ackWarning, setAckWarning] = useState<string | null>(null);

  // Reset form and scrub secret from component memory
  const handleDismiss = useCallback(() => {
    setRawKey(null);
    setCreatedMetadata(null);
    setStep("form");
    setName("");
    setExpiryMode("never");
    setCustomExpiry("");
    setNameError(null);
    setExpiryError(null);
    setSubmitError(null);
    setIsSubmitting(false);
    isSubmittingRef.current = false;
    setHasAcknowledged(false);
    setHasCopied(false);
    setCopyError(null);
    setAckWarning(null);
    setIsSecretVisible(false);
    onClose();
  }, [onClose]);

  // Attempt to close: blocks dismissal in success step unless acknowledged
  const handleAttemptClose = useCallback(() => {
    if (step === "success" && !hasAcknowledged) {
      setAckWarning("You must confirm you have safely stored this key before closing.");
      return;
    }
    handleDismiss();
  }, [step, hasAcknowledged, handleDismiss]);

  // Session expiry / logout: scrub secret immediately and close
  useEffect(() => {
    if (!auth.isAuthenticated && isOpen) {
      handleDismiss();
    }
  }, [auth.isAuthenticated, isOpen, handleDismiss]);

  // Route teardown / unmount safety: clear memory reference
  useEffect(() => {
    return () => {
      setRawKey(null);
      setCreatedMetadata(null);
      isSubmittingRef.current = false;
    };
  }, []);

  // Validation helper
  const validate = (): boolean => {
    let isValid = true;
    const trimmed = name.trim();
    if (!trimmed) {
      setNameError("Key name is required.");
      isValid = false;
    } else if (new TextEncoder().encode(name).length > 256) {
      setNameError("Key name must be 256 bytes or fewer.");
      isValid = false;
    } else {
      setNameError(null);
    }

    if (expiryMode === "custom") {
      const err = validateCustomExpiry(customExpiry);
      setExpiryError(err);
      if (err) {
        isValid = false;
      }
    } else {
      setExpiryError(null);
    }

    return isValid;
  };

  // Pre-submit cancellation
  const handleCancel = () => {
    if (isSubmitting) return;
    handleDismiss();
  };

  // Form submit handler with duplicate-submit prevention
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    if (isSubmitting || isSubmittingRef.current) {
      return;
    }

    if (!validate()) {
      return;
    }

    isSubmittingRef.current = true;
    setIsSubmitting(true);
    setSubmitError(null);

    let expiresAt: string | undefined;
    if (expiryMode !== "never") {
      expiresAt = computeExpiresAt(expiryMode, customExpiry);
    }

    try {
      const response = await createKey({
        name: name.trim(),
        expires_at: expiresAt,
      });

      // Retain raw key strictly in component memory
      setRawKey(response.key);
      setCreatedMetadata({
        id: response.id,
        name: response.name,
        prefix: response.prefix,
        created_at: response.created_at,
        expires_at: response.expires_at,
      });
      setStep("success");

      // Refresh inventory data only after creation succeeds
      void queryClient.invalidateQueries({ queryKey: keyQueryKeys.lists() });
      onSuccess?.({
        id: response.id,
        name: response.name,
        prefix: response.prefix,
      });
    } catch (err: unknown) {
      // Errors leave form open and do not invent key state
      const message =
        err && typeof err === "object" && "message" in err
          ? String(err.message)
          : "Failed to create API key";
      setSubmitError(message);
    } finally {
      setIsSubmitting(false);
      isSubmittingRef.current = false;
    }
  };

  // Explicit copy action
  const handleCopy = async () => {
    if (!rawKey) return;
    try {
      if (
        typeof navigator === "undefined" ||
        !navigator.clipboard ||
        typeof navigator.clipboard.writeText !== "function"
      ) {
        throw new Error("Clipboard API unavailable");
      }
      await navigator.clipboard.writeText(rawKey);
      setHasCopied(true);
      setCopyError(null);
      setTimeout(() => setHasCopied(false), 2500);
    } catch {
      setCopyError("Failed to copy key to clipboard. Please copy manually or download.");
    }
  };

  // Explicit download action
  const handleDownload = () => {
    if (!rawKey || !createdMetadata) return;
    const filename = generateKeyDownloadFilename(
      createdMetadata.name,
      createdMetadata.prefix || createdMetadata.id
    );
    downloadKeySecret(filename, rawKey);
  };

  if (!isOpen) return null;

  return (
    <Dialog
      isOpen={isOpen}
      onClose={handleAttemptClose}
      title={step === "form" ? "Create API Key" : "API Key Created"}
      description={
        step === "form"
          ? "Generate a new client key for OpenAI-compatible gateway access."
          : "Save your API key now. It will never be shown again."
      }
      size="md"
      closeOnBackdropClick={step === "form" && !isSubmitting}
      footer={
        step === "form" ? (
          <div
            style={{
              display: "flex",
              justifyContent: "flex-end",
              gap: "var(--space-2)",
              width: "100%",
            }}
          >
            <Button
              variant="ghost"
              onClick={handleCancel}
              disabled={isSubmitting}
              data-testid="create-key-cancel-btn"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              variant="primary"
              isLoading={isSubmitting}
              disabled={isSubmitting}
              leftIcon={<Plus size={16} aria-hidden="true" />}
              onClick={handleSubmit}
              data-testid="create-key-submit-btn"
            >
              {isSubmitting ? "Creating Key..." : "Create Key"}
            </Button>
          </div>
        ) : (
          <div
            style={{
              display: "flex",
              justifyContent: "flex-end",
              width: "100%",
            }}
          >
            <Button
              variant="primary"
              disabled={!hasAcknowledged}
              onClick={handleDismiss}
              data-testid="done-secret-btn"
            >
              Done
            </Button>
          </div>
        )
      }
    >
      {step === "form" ? (
        <form
          onSubmit={handleSubmit}
          className="gw-create-key-form"
          data-testid="create-key-form"
          noValidate
        >
          {submitError && (
            <Alert
              variant="danger"
              title="Key Creation Failed"
              data-testid="create-key-submit-error"
            >
              {submitError}
            </Alert>
          )}

          <Input
            label="Key Name"
            placeholder="e.g. backend-worker-prod"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (nameError) setNameError(null);
            }}
            onBlur={() => {
              if (!name.trim()) {
                setNameError("Key name is required.");
              } else if (new TextEncoder().encode(name).length > 256) {
                setNameError("Key name must be 256 bytes or fewer.");
              }
            }}
            error={nameError || undefined}
            disabled={isSubmitting}
            required
            helperText="A recognizable label for identifying this key in telemetry and policy management."
            data-testid="create-key-name-input"
            autoFocus
          />

          <Select
            label="Expiration"
            value={expiryMode}
            onChange={(e) => {
              setExpiryMode(e.target.value as ExpiryMode);
              setExpiryError(null);
            }}
            options={EXPIRY_OPTIONS}
            disabled={isSubmitting}
            helperText="Keys automatically stop accepting requests once expired."
            data-testid="create-key-expiry-select"
          />

          {expiryMode === "custom" && (
            <Input
              type="datetime-local"
              label="Custom Expiration Date & Time (UTC)"
              value={customExpiry}
              onChange={(e) => {
                setCustomExpiry(e.target.value);
                if (expiryError) setExpiryError(null);
              }}
              onBlur={() => {
                const err = validateCustomExpiry(customExpiry);
                if (err) {
                  setExpiryError(err);
                }
              }}
              error={expiryError || undefined}
              disabled={isSubmitting}
              helperText="Specify the UTC date and time when this key should expire."
              data-testid="create-key-custom-expiry-input"
            />
          )}
        </form>
      ) : (
        <div
          className="gw-secret-handoff-container"
          data-testid="secret-handoff-step"
        >
          {/* Warning banner */}
          <Alert
            variant="warning"
            title="Important: Save Your API Key"
            data-testid="secret-warning-alert"
          >
            Store this API key in a secure location now. You will not be able to see or copy it again once this window is closed.
          </Alert>

          {/* Metadata summary */}
          {createdMetadata && (
            <div className="gw-secret-meta-grid" data-testid="secret-meta-summary">
              <div className="gw-secret-meta-item">
                <span className="gw-secret-meta-label">Name</span>
                <span className="gw-secret-meta-value">{createdMetadata.name}</span>
              </div>
              <div className="gw-secret-meta-item">
                <span className="gw-secret-meta-label">Key ID</span>
                <span className="gw-secret-meta-value" style={{ fontFamily: "var(--font-mono)", fontSize: "var(--font-size-xs)" }}>
                  {createdMetadata.id}
                </span>
              </div>
              <div className="gw-secret-meta-item">
                <span className="gw-secret-meta-label">Display Prefix</span>
                <span className="gw-secret-meta-value" style={{ fontFamily: "var(--font-mono)" }}>
                  {createdMetadata.prefix}
                </span>
              </div>
              <div className="gw-secret-meta-item">
                <span className="gw-secret-meta-label">Expiration</span>
                <span className="gw-secret-meta-value">
                  {createdMetadata.expires_at
                    ? formatTimestamp(createdMetadata.expires_at)
                    : "Never expires"}
                </span>
              </div>
            </div>
          )}

          {/* Raw Secret Credential Box */}
          <div className="gw-secret-box" data-testid="secret-region">
            <label htmlFor="gw-secret-display-input" className="gw-form-label">
              Raw API Key Secret
            </label>
            <div className="gw-secret-input-wrapper">
              <input
                id="gw-secret-display-input"
                name="gw-secret-token"
                type={isSecretVisible ? "text" : "password"}
                readOnly
                value={rawKey || ""}
                className="gw-input gw-secret-input"
                data-testid="secret-key-display"
                data-1p-ignore="true"
                data-lpignore="true"
                data-bwignore="true"
                data-form-type="other"
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
              />
              <IconButton
                icon={isSecretVisible ? <EyeOff size={16} /> : <Eye size={16} />}
                aria-label={isSecretVisible ? "Hide key secret" : "Show key secret"}
                variant="secondary"
                size="sm"
                onClick={() => setIsSecretVisible((prev) => !prev)}
                data-testid="toggle-visibility-btn"
              />
            </div>
          </div>

          {/* Action buttons: Explicit copy and plain-text download */}
          <div className="gw-secret-actions-row">
            <Button
              variant="secondary"
              size="sm"
              leftIcon={hasCopied ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
              onClick={handleCopy}
              data-testid="copy-key-btn"
            >
              {hasCopied ? "Copied!" : "Copy Key"}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              leftIcon={<Download size={16} aria-hidden="true" />}
              onClick={handleDownload}
              data-testid="download-key-btn"
            >
              Download (.txt)
            </Button>
          </div>

          {/* Screen reader polite copy announcement */}
          <div aria-live="polite" className="sr-only" data-testid="copy-live-region">
            {hasCopied ? "API key copied to clipboard" : ""}
          </div>

          {/* Copy error feedback if clipboard fails */}
          {copyError && (
            <Alert variant="danger" data-testid="copy-error-alert">
              {copyError}
            </Alert>
          )}

          {/* Mandatory acknowledgement requirement */}
          <div className="gw-secret-acknowledgement">
            <Checkbox
              id="ack-stored-checkbox"
              checked={hasAcknowledged}
              onChange={(e) => {
                setHasAcknowledged(e.target.checked);
                if (e.target.checked) {
                  setAckWarning(null);
                }
              }}
              label="I have saved this API key in a secure location"
              description="I understand that 9Gateway does not store raw secrets and this key cannot be retrieved again."
              data-testid="ack-stored-checkbox"
            />
          </div>

          {/* Warning when attempting to close unacknowledged */}
          {ackWarning && (
            <Alert variant="danger" data-testid="ack-warning-alert">
              {ackWarning}
            </Alert>
          )}
        </div>
      )}
    </Dialog>
  );
};
