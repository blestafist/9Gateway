import React, { useState, useEffect, useMemo, useCallback } from "react";
import { Download, Copy, Check, X, AlertTriangle, FileText, Binary, Code2 } from "lucide-react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
  IconButton,
  Badge,
  Alert,
  Skeleton,
} from "../../../shared/ui";
import { formatByteCount, formatExactByteCount } from "../../../shared/formatters";
import { RequestBodyContent, RequestBodyKind } from "../types";
import { getRequestBody } from "../api";
import {
  formatBodyKindLabel,
  formatRawTextPreview,
  formatHexPreview,
  tryFormatPrettyJson,
  downloadBodyBytes,
  MAX_PREVIEW_BYTES,
  truncateId,
} from "../helpers";

export interface BodyViewerProps {
  requestId: string;
  availableKinds: RequestBodyKind[];
  initialKind?: RequestBodyKind;
  onClose: () => void;
}

type ViewMode = "text" | "hex" | "json";

export const BodyViewer: React.FC<BodyViewerProps> = ({
  requestId,
  availableKinds,
  initialKind,
  onClose,
}) => {
  const [selectedKind, setSelectedKind] = useState<RequestBodyKind>(() => {
    if (initialKind && availableKinds.includes(initialKind)) {
      return initialKind;
    }
    return availableKinds[0] || "response";
  });

  const [bodyContent, setBodyContent] = useState<RequestBodyContent | null>(null);
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [is404, setIs404] = useState<boolean>(false);
  const [viewMode, setViewMode] = useState<ViewMode>("text");
  const [copied, setCopied] = useState<boolean>(false);

  // Fetch selected kind on explicit action or selection change
  useEffect(() => {
    const controller = new AbortController();
    setIsLoading(true);
    setErrorMessage(null);
    setIs404(false);
    setBodyContent(null);

    getRequestBody(requestId, selectedKind, controller.signal)
      .then((content) => {
        setBodyContent(content);
        setIsLoading(false);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        setIsLoading(false);

        const status =
          err && typeof err === "object" && "status" in err
            ? (err as { status?: number }).status
            : undefined;

        if (status === 404) {
          setIs404(true);
          setErrorMessage(
            "Captured body not found. It may have been removed by history retention or was never captured."
          );
        } else {
          setErrorMessage(
            "Failed to load captured body from the gateway admin service."
          );
        }
      });

    // Cleanup: abort in-flight fetch and scrub memory references immediately
    return () => {
      controller.abort();
      setBodyContent(null);
    };
  }, [requestId, selectedKind]);

  const rawBytes = useMemo(() => {
    if (!bodyContent) return new Uint8Array(0);
    return bodyContent.bytes ?? new TextEncoder().encode(bodyContent.data);
  }, [bodyContent]);

  // Derived previews bounded to MAX_PREVIEW_BYTES
  const textPreview = useMemo(() => {
    if (!bodyContent) return { text: "", isPreviewTruncated: false };
    return formatRawTextPreview(rawBytes, MAX_PREVIEW_BYTES);
  }, [bodyContent, rawBytes]);

  const hexPreview = useMemo(() => {
    if (!bodyContent) return { hex: "", isPreviewTruncated: false };
    return formatHexPreview(rawBytes, MAX_PREVIEW_BYTES);
  }, [bodyContent, rawBytes]);

  const jsonResult = useMemo(() => {
    if (!bodyContent) return { isValid: false, pretty: null };
    return tryFormatPrettyJson(bodyContent.data);
  }, [bodyContent]);

  // Fall back to text preview if viewMode is json but content is not valid JSON (e.g. tab switch)
  const activeViewMode: ViewMode =
    viewMode === "json" && !jsonResult.isValid ? "text" : viewMode;

  // Default to JSON tab if valid JSON on load or tab switch, or fallback to text if non-JSON
  useEffect(() => {
    if (jsonResult.isValid && viewMode === "text") {
      setViewMode("json");
    } else if (!jsonResult.isValid && viewMode === "json") {
      setViewMode("text");
    }
  }, [selectedKind, jsonResult.isValid]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleDownload = useCallback(() => {
    if (!bodyContent) return;
    const ext =
      jsonResult.isValid && activeViewMode === "json"
        ? "json"
        : bodyContent.content_type.includes("json")
          ? "json"
          : "bin";
    const filename = `request-${truncateId(requestId)}-${selectedKind}.${ext}`;
    downloadBodyBytes(rawBytes, filename, bodyContent.content_type);
  }, [bodyContent, jsonResult.isValid, activeViewMode, requestId, selectedKind, rawBytes]);

  const handleCopyText = useCallback(() => {
    const textToCopy =
      activeViewMode === "json" && jsonResult.pretty
        ? jsonResult.pretty
        : activeViewMode === "hex"
          ? hexPreview.hex
          : textPreview.text;

    if (typeof navigator !== "undefined" && navigator.clipboard) {
      void navigator.clipboard.writeText(textToCopy);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }, [activeViewMode, jsonResult.pretty, hexPreview.hex, textPreview.text]);

  const isEmpty = rawBytes.length === 0;

  return (
    <Card className="gw-body-viewer-card" data-testid="body-viewer">
      <CardHeader>
        <div className="gw-body-viewer-header">
          <div className="gw-body-viewer-title-group">
            <CardTitle>Captured Payload Viewer</CardTitle>
            <span className="gw-table-dimmed gw-body-authoritative-note">
              Authoritative raw bytes: {formatExactByteCount(rawBytes.length)}
            </span>
          </div>

          <div className="gw-body-viewer-actions">
            {bodyContent && (
              <Button
                variant="secondary"
                size="sm"
                leftIcon={<Download size={14} />}
                onClick={handleDownload}
                data-testid="download-body-btn"
              >
                Download Exact Bytes
              </Button>
            )}

            <IconButton
              icon={<X size={16} aria-hidden="true" />}
              aria-label="Close captured body viewer"
              variant="ghost"
              size="sm"
              onClick={onClose}
              data-testid="close-body-viewer-btn"
            />
          </div>
        </div>
      </CardHeader>

      <CardContent>
        {/* Kind Selector Tabs (if multiple available) */}
        {availableKinds.length > 1 && (
          <div
            className="gw-body-kind-tabs"
            role="tablist"
            aria-label="Captured body kinds"
            data-testid="body-kind-tabs"
          >
            {availableKinds.map((kind) => {
              const isSelected = kind === selectedKind;
              return (
                <button
                  key={kind}
                  type="button"
                  role="tab"
                  aria-selected={isSelected}
                  aria-controls={`body-panel-${kind}`}
                  id={`body-tab-${kind}`}
                  className={`gw-body-kind-tab ${isSelected ? "gw-body-kind-tab-active" : ""}`}
                  onClick={() => setSelectedKind(kind)}
                  data-testid={`body-tab-${kind}`}
                >
                  {formatBodyKindLabel(kind)}
                </button>
              );
            })}
          </div>
        )}

        {/* Loading state */}
        {isLoading && (
          <div
            className="gw-body-loading-skeleton"
            data-testid="body-loading-skeleton"
          >
            <Skeleton height="36px" />
            <Skeleton height="180px" />
          </div>
        )}

        {/* Error State */}
        {!isLoading && errorMessage && (
          <Alert
            variant={is404 ? "warning" : "danger"}
            icon={<AlertTriangle size={18} />}
            title={is404 ? "Body Not Available" : "Failed to Load Body"}
            data-testid="body-error-alert"
          >
            {errorMessage}
          </Alert>
        )}

        {/* Loaded Content */}
        {!isLoading && bodyContent && (
          <div
            className="gw-body-content-panel"
            role="tabpanel"
            id={`body-panel-${selectedKind}`}
            aria-labelledby={`body-tab-${selectedKind}`}
            data-testid="body-content-panel"
          >
            {/* Metadata Bar */}
            <div className="gw-body-metadata-bar" data-testid="body-metadata-bar">
              <div className="gw-body-metadata-items">
                <div className="gw-body-meta-item">
                  <span className="gw-detail-label">Kind:</span>
                  <Badge variant="neutral" size="sm">
                    {formatBodyKindLabel(bodyContent.kind)}
                  </Badge>
                </div>
                <div className="gw-body-meta-item">
                  <span className="gw-detail-label">Content-Type:</span>
                  <code className="gw-content-type-val">{bodyContent.content_type}</code>
                </div>
                <div className="gw-body-meta-item">
                  <span className="gw-detail-label">Captured Size:</span>
                  <span>{formatExactByteCount(rawBytes.length)}</span>
                </div>
                <div className="gw-body-meta-item">
                  <span className="gw-detail-label">Original Size:</span>
                  <span>{formatExactByteCount(bodyContent.original_size)}</span>
                </div>
              </div>

              {/* View Mode Switcher */}
              <div className="gw-body-view-controls">
                <div
                  className="gw-view-mode-buttons"
                  role="group"
                  aria-label="Preview representation"
                >
                  <Button
                    variant={activeViewMode === "text" ? "primary" : "ghost"}
                    size="sm"
                    leftIcon={<FileText size={13} />}
                    onClick={() => setViewMode("text")}
                    data-testid="view-mode-text"
                  >
                    Raw Text
                  </Button>
                  <Button
                    variant={activeViewMode === "hex" ? "primary" : "ghost"}
                    size="sm"
                    leftIcon={<Binary size={13} />}
                    onClick={() => setViewMode("hex")}
                    data-testid="view-mode-hex"
                  >
                    Hex
                  </Button>
                  {jsonResult.isValid && (
                    <Button
                      variant={activeViewMode === "json" ? "primary" : "ghost"}
                      size="sm"
                      leftIcon={<Code2 size={13} />}
                      onClick={() => setViewMode("json")}
                      data-testid="view-mode-json"
                    >
                      Pretty JSON
                    </Button>
                  )}
                </div>

                <Button
                  variant="ghost"
                  size="sm"
                  leftIcon={
                    copied ? (
                      <Check size={13} aria-hidden="true" />
                    ) : (
                      <Copy size={13} aria-hidden="true" />
                    )
                  }
                  onClick={handleCopyText}
                  data-testid="copy-body-text-btn"
                  aria-label={copied ? "Copied preview text" : "Copy preview text"}
                >
                  {copied ? "Copied" : "Copy"}
                </Button>
              </div>
            </div>

            {/* Truncation Warning */}
            {bodyContent.truncated && (
              <Alert
                variant="warning"
                icon={<AlertTriangle size={16} />}
                title="Body Truncated by Gateway"
                data-testid="body-truncated-alert"
              >
                Payload exceeded the configured body capture limit. Captured first{" "}
                <strong>{formatExactByteCount(rawBytes.length)}</strong> of{" "}
                <strong>{formatExactByteCount(bodyContent.original_size)}</strong> original bytes.
              </Alert>
            )}

            {/* Preview Bound Warning (First 256 KiB) */}
            {(textPreview.isPreviewTruncated || hexPreview.isPreviewTruncated) && (
              <Alert
                variant="info"
                title="Preview Bounded to 256 KiB"
                data-testid="body-preview-bound-alert"
              >
                Preview rendering is capped at the first 256 KiB ({formatByteCount(MAX_PREVIEW_BYTES)})
                to ensure layout performance. Download exact bytes to inspect full content.
              </Alert>
            )}

            {/* Empty Body Notice */}
            {isEmpty && (
              <div className="gw-body-empty-notice" data-testid="body-empty-notice">
                Captured body is empty (0 bytes).
              </div>
            )}

            {/* Code Previews: NEVER inject HTML, pure React text child */}
            {!isEmpty && (
              <div className="gw-body-pre-container" data-testid="body-preview-container">
                {activeViewMode === "text" && (
                  <pre
                    className="gw-body-pre"
                    tabIndex={0}
                    data-testid="body-pre-text"
                  >
                    <code>{textPreview.text}</code>
                  </pre>
                )}

                {activeViewMode === "hex" && (
                  <pre
                    className="gw-body-pre gw-body-hex-pre"
                    tabIndex={0}
                    data-testid="body-pre-hex"
                  >
                    <code>{hexPreview.hex}</code>
                  </pre>
                )}

                {activeViewMode === "json" && jsonResult.pretty && (
                  <pre
                    className="gw-body-pre"
                    tabIndex={0}
                    data-testid="body-pre-json"
                  >
                    <code>{jsonResult.pretty}</code>
                  </pre>
                )}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
};

export default BodyViewer;
