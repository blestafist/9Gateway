import React, { useState, useEffect, useMemo, useRef } from "react";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  StatusPill,
  Badge,
  Button,
  Alert,
  Skeleton,
  Dialog,
  useToast,
} from "../../shared/ui";
import { formatTimestamp } from "../../shared/formatters/timestamp";
import { formatDurationSeconds } from "../../shared/formatters/duration";
import { formatByteCount } from "../../shared/formatters/bytes";
import { useAuth } from "../auth";
import { AdminApiError, OfflineError } from "../../shared/transport";
import { getSystem } from "./api";
import { systemQueryKeys } from "./queryKeys";
import { SystemResponse, ReadinessCheck } from "./types";
import {
  Server,
  Database,
  Shield,
  Activity,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  HelpCircle,
  RefreshCw,
  Copy,
  ExternalLink,
  WifiOff,
  Check,
} from "lucide-react";
import "./system.css";

function formatUptime(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return "—";
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const h = Math.floor(m / 60);
  const d = Math.floor(h / 24);
  if (d > 0) {
    const remH = h % 24;
    return `${d}d ${remH}h`;
  }
  if (h > 0) {
    const remM = m % 60;
    return `${h}h ${remM}m`;
  }
  const remS = seconds % 60;
  return `${m}m ${remS}s`;
}

function getCheckDocLink(name: string): { label: string; href: string } | null {
  switch (name) {
    case "sqlite":
    case "schema":
      return { label: "Storage operations", href: "https://github.com/pestit/9Gateway#storage" };
    case "telemetry":
      return { label: "Telemetry architecture", href: "https://github.com/pestit/9Gateway#telemetry" };
    case "upstream":
      return { label: "Upstream deployment", href: "https://github.com/pestit/9Gateway#deployment" };
    case "lifecycle":
      return { label: "Lifecycle guide", href: "https://github.com/pestit/9Gateway#server-lifecycle" };
    default:
      return null;
  }
}

/**
 * Build the exact allowlisted non-secret diagnostics text to preview and copy.
 */
export function buildDiagnosticsText(data: SystemResponse): string {
  const checkSummary = Object.entries(data.readiness.checks)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => {
      const msg = v.message ? ` (${v.message})` : "";
      return `  - ${k}: ${v.status}${msg}`;
    })
    .join("\n");

  return [
    `=== 9Gateway System Diagnostics ===`,
    `Generated: ${new Date().toISOString()}`,
    ``,
    `[Build & Runtime]`,
    `Version: ${data.version}`,
    `Commit: ${data.commit}`,
    `Build Date: ${data.build_time}`,
    `Process Start: ${data.start_time}`,
    `Uptime Seconds: ${data.uptime_seconds}`,
    ``,
    `[Health & Readiness]`,
    `Overall Ready: ${data.ready ? "true" : "false"}`,
    `Checks:`,
    checkSummary || "  (none)",
    ``,
    `[Storage & Database]`,
    `Status: ${data.storage.status}`,
    `Healthy: ${data.storage.healthy ? "true" : "false"}`,
    `Schema Version: ${data.storage.schema_version !== null ? data.storage.schema_version : "null"}`,
    `Current Schema Version: ${data.storage.current_schema_version}`,
    ``,
    `[Telemetry & Queue]`,
    `Active Requests: ${data.active_requests}`,
    `Queue Depth: ${data.telemetry.queue_depth}`,
    `Queue Capacity: ${data.telemetry.queue_capacity}`,
    `Dropped Records: ${data.telemetry.dropped_records}`,
    ``,
    `[Operational Limits]`,
    `Request Retention Seconds: ${data.limits.request_retention_seconds}`,
    `Body Retention Seconds: ${data.limits.body_retention_seconds}`,
    `Max Captured Body Bytes: ${data.limits.max_captured_body_bytes}`,
  ].join("\n");
}

export const SystemPage: React.FC = () => {
  const { isAuthenticated } = useAuth();
  const toast = useToast();

  const [isOnline, setIsOnline] = useState<boolean>(() =>
    typeof navigator !== "undefined" ? navigator.onLine : true
  );
  const [isVisible, setIsVisible] = useState<boolean>(() =>
    typeof document !== "undefined" ? document.visibilityState === "visible" : true
  );

  useEffect(() => {
    const handleOnline = () => setIsOnline(true);
    const handleOffline = () => setIsOnline(false);
    const handleVisibility = () => {
      setIsVisible(document.visibilityState === "visible");
    };

    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);
    document.addEventListener("visibilitychange", handleVisibility);

    return () => {
      window.removeEventListener("online", handleOnline);
      window.removeEventListener("offline", handleOffline);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, []);

  // Poll strictly >= 60s and only while visible and authenticated
  const shouldPoll = isAuthenticated && isOnline && isVisible;

  const query = useQuery({
    queryKey: systemQueryKeys.info(),
    queryFn: ({ signal }) => getSystem(signal),
    enabled: isAuthenticated,
    refetchInterval: shouldPoll ? 60_000 : false,
    refetchIntervalInBackground: false,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });

  const handleManualRefresh = () => {
    if (query.isFetching) return;
    query.refetch();
  };

  // Diagnostics preview modal state
  const [isDiagnosticsModalOpen, setIsDiagnosticsModalOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const copiedTimeoutRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (copiedTimeoutRef.current) {
        window.clearTimeout(copiedTimeoutRef.current);
      }
    };
  }, []);

  const diagnosticsText = useMemo(() => {
    if (!query.data) return "";
    return buildDiagnosticsText(query.data);
  }, [query.data]);

  const handleCopyDiagnostics = async () => {
    if (!diagnosticsText) return;
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(diagnosticsText);
        setCopied(true);
        toast.show({ title: "Diagnostics summary copied to clipboard", variant: "info" });
        if (copiedTimeoutRef.current) {
          window.clearTimeout(copiedTimeoutRef.current);
        }
        copiedTimeoutRef.current = window.setTimeout(() => {
          setCopied(false);
        }, 2000);
      } else {
        throw new Error("Clipboard API unavailable");
      }
    } catch {
      toast.show({ title: "Failed to copy diagnostics. Select and copy preview manually.", variant: "danger" });
    }
  };

  const freshnessInfo = useMemo(() => {
    if (!isOnline) {
      return {
        text: "Offline — automatic polling paused",
        className: "gw-system-freshness-text--offline",
      };
    }
    if (!isVisible) {
      return {
        text: "Tab inactive — updates paused",
        className: "",
      };
    }
    if (query.isFetching && query.data) {
      return {
        text: "Refreshing in background…",
        className: "gw-system-freshness-text--stale",
      };
    }
    if (query.isError && query.data) {
      return {
        text: "Stale snapshot — refresh failed",
        className: "gw-system-freshness-text--stale",
      };
    }
    if (query.data) {
      return {
        text: `Updated at ${formatTimestamp(new Date().toISOString())}`,
        className: "",
      };
    }
    return {
      text: "Loading…",
      className: "",
    };
  }, [isOnline, isVisible, query.isFetching, query.isError, query.data]);

  // Handle fatal error state (no snapshot available)
  if (query.isError && !query.data) {
    const error = query.error;
    const isAuthError = error instanceof AdminApiError && error.isAuthError;
    const isOfflineErr = !isOnline || error instanceof OfflineError;

    return (
      <div className="gw-page-content gw-system-container" data-testid="system-page">
        <div className="gw-system-header">
          <div className="gw-system-title-area">
            <div className="gw-system-title-row">
              <h1 className="gw-system-page-title">System &amp; Diagnostics</h1>
              <Badge variant="danger" dot>Unavailable</Badge>
            </div>
            <p className="gw-system-subtitle">Runtime health, build identity, storage, and limits</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={handleManualRefresh}
            disabled={query.isFetching}
            data-testid="system-refresh-btn"
          >
            <RefreshCw size={14} className={query.isFetching ? "gw-spinner" : ""} />
            <span>Retry</span>
          </Button>
        </div>

        <Alert
          variant="danger"
          title={isAuthError ? "Session Expired" : isOfflineErr ? "Network Connection Lost" : "Failed to Load System Diagnostics"}
          icon={isOfflineErr ? <WifiOff size={18} /> : <AlertTriangle size={18} />}
        >
          {isAuthError
            ? "Your operator session has expired or is unauthenticated. Please log in again to inspect system internals."
            : isOfflineErr
            ? "You are currently offline. Check your network connection and try again."
            : error.message || "An unexpected error occurred while communicating with the gateway."}
        </Alert>
      </div>
    );
  }

  // Loading state (initial load without cached data)
  if (query.isLoading && !query.data) {
    return (
      <div className="gw-page-content gw-system-container" data-testid="system-page">
        <div className="gw-system-header">
          <div className="gw-system-title-area">
            <h1 className="gw-system-page-title">System &amp; Diagnostics</h1>
            <p className="gw-system-subtitle">Runtime health, build identity, storage, and limits</p>
          </div>
        </div>
        <div className="gw-system-grid">
          <Card>
            <CardHeader><Skeleton style={{ height: "24px", width: "160px" }} /></CardHeader>
            <CardContent><Skeleton style={{ height: "120px" }} /></CardContent>
          </Card>
          <Card>
            <CardHeader><Skeleton style={{ height: "24px", width: "160px" }} /></CardHeader>
            <CardContent><Skeleton style={{ height: "120px" }} /></CardContent>
          </Card>
          <Card>
            <CardHeader><Skeleton style={{ height: "24px", width: "160px" }} /></CardHeader>
            <CardContent><Skeleton style={{ height: "120px" }} /></CardContent>
          </Card>
          <Card>
            <CardHeader><Skeleton style={{ height: "24px", width: "160px" }} /></CardHeader>
            <CardContent><Skeleton style={{ height: "120px" }} /></CardContent>
          </Card>
        </div>
      </div>
    );
  }

  const data = query.data!;

  // Health and readiness computations
  const isHealthy = data.ready;
  const isDevBuild = data.version === "dev" || data.version.includes("-dev");
  const isTelemetrySaturated = data.telemetry.queue_capacity > 0 &&
    (data.telemetry.queue_depth / data.telemetry.queue_capacity >= 0.9 || data.telemetry.dropped_records > 0);
  const isStorageDegraded = data.storage.status === "degraded" || !data.storage.healthy;
  const isStorageUnavailable = data.storage.status === "unavailable";

  return (
    <div className="gw-page-content gw-system-container" data-testid="system-page">
      {/* Header with Title, Status Badge, Freshness, and Actions */}
      <div className="gw-system-header">
        <div className="gw-system-title-area">
          <div className="gw-system-title-row">
            <h1 className="gw-system-page-title">System &amp; Diagnostics</h1>
            {isHealthy ? (
              <Badge variant="success" dot data-testid="system-status-badge">Healthy</Badge>
            ) : (
              <Badge variant="warning" dot data-testid="system-status-badge">Degraded</Badge>
            )}
            {isDevBuild && (
              <Badge variant="neutral" size="sm" data-testid="dev-build-badge">Development Build</Badge>
            )}
          </div>
          <p className="gw-system-subtitle">Runtime health, build identity, storage, and limits</p>
        </div>

        <div className="gw-system-actions">
          <div className="gw-system-freshness" data-testid="system-freshness-info">
            <span className={freshnessInfo.className}>{freshnessInfo.text}</span>
          </div>

          <Button
            variant="outline"
            size="sm"
            onClick={() => setIsDiagnosticsModalOpen(true)}
            data-testid="system-diagnostics-btn"
            aria-label="View and copy diagnostics summary"
          >
            <Copy size={14} aria-hidden="true" />
            <span>Diagnostics Summary</span>
          </Button>

          <Button
            variant="outline"
            size="sm"
            onClick={handleManualRefresh}
            disabled={query.isFetching}
            aria-busy={query.isFetching}
            aria-label="Refresh system diagnostics"
            data-testid="system-refresh-btn"
          >
            <RefreshCw
              size={14}
              className={query.isFetching ? "gw-spinner" : ""}
              aria-hidden="true"
            />
            <span>Refresh</span>
          </Button>
        </div>
      </div>

      {/* Stale snapshot alert when background refetch fails */}
      {query.isError && query.data && (
        <Alert
          variant="warning"
          title="Background Refresh Failed"
          icon={<AlertTriangle size={16} />}
          className="gw-system-banner"
          action={
            <Button
              variant="outline"
              size="sm"
              onClick={handleManualRefresh}
              disabled={query.isFetching}
            >
              Retry Refresh
            </Button>
          }
        >
          {query.error.message || "Failed to fetch latest system state."} Displaying previous operational snapshot.
        </Alert>
      )}

      {/* System Sections Grid */}
      <div className="gw-system-grid">
        {/* Section 1: Health & Individual Readiness Checks */}
        <Card data-testid="readiness-card">
          <CardHeader>
            <div className="gw-system-card-title">
              <Server size={18} style={{ color: "var(--accent-primary)" }} aria-hidden="true" />
              <CardTitle>Health &amp; Readiness</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="gw-system-card-content">
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Overall Gateway</span>
                {data.ready ? (
                  <StatusPill label="Ready" variant="success" />
                ) : (
                  <StatusPill label="Not Ready" variant="warning" />
                )}
              </div>
            </div>

            <div className="gw-system-check-list" role="list" aria-label="Individual readiness checks">
              {Object.entries(data.readiness.checks).map(([name, check]: [string, ReadinessCheck]) => {
                const doc = getCheckDocLink(name);
                const isPass = check.status === "pass";
                const isFail = check.status === "fail";
                return (
                  <div key={name} className="gw-system-check-item" role="listitem" data-testid={`check-${name}`}>
                    <div className="gw-system-check-header">
                      <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                        {isPass ? (
                          <CheckCircle2 size={14} style={{ color: "var(--status-success)" }} aria-hidden="true" />
                        ) : isFail ? (
                          <XCircle size={14} style={{ color: "var(--status-error)" }} aria-hidden="true" />
                        ) : (
                          <HelpCircle size={14} style={{ color: "var(--status-warning)" }} aria-hidden="true" />
                        )}
                        <span className="gw-system-check-name">{name}</span>
                      </div>
                      <Badge
                        variant={isPass ? "success" : isFail ? "danger" : "warning"}
                        size="sm"
                      >
                        {check.status.toUpperCase()}
                      </Badge>
                    </div>

                    {check.message && (
                      <div
                        className={`gw-system-check-message ${isFail ? "gw-system-check-message--fail" : ""}`}
                        data-testid={`check-message-${name}`}
                      >
                        {check.message}
                      </div>
                    )}

                    {!isPass && doc && (
                      <div>
                        <a
                          href={doc.href}
                          target="_blank"
                          rel="noreferrer noopener"
                          className="gw-system-doc-link"
                          aria-label={`${name} documentation: ${doc.label}`}
                        >
                          <span>{doc.label}</span>
                          <ExternalLink size={12} aria-hidden="true" />
                        </a>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </CardContent>
        </Card>

        {/* Section 2: Build & Runtime Details */}
        <Card data-testid="build-runtime-card">
          <CardHeader>
            <div className="gw-system-card-title">
              <Activity size={18} style={{ color: "var(--metric-tokens-in)" }} aria-hidden="true" />
              <CardTitle>Build &amp; Runtime</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="gw-system-card-content">
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Version</span>
                <code className="gw-code-id" data-testid="system-version">{data.version}</code>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Commit</span>
                <code className="gw-code-id" data-testid="system-commit">{data.commit}</code>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Build Time</span>
                <span className="gw-system-v" data-testid="system-build-time">
                  {data.build_time === "unknown" ? "Unknown" : formatTimestamp(data.build_time)}
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Started At</span>
                <span className="gw-system-v" data-testid="system-start-time">
                  {formatTimestamp(data.start_time)}
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Uptime</span>
                <span className="gw-system-v" data-testid="system-uptime">
                  {formatUptime(data.uptime_seconds)}
                </span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Section 3: Telemetry & Ingress Pressure */}
        <Card data-testid="telemetry-card">
          <CardHeader>
            <div className="gw-system-card-title">
              <Shield size={18} style={{ color: "var(--metric-cost)" }} aria-hidden="true" />
              <CardTitle>Telemetry &amp; Ingress</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="gw-system-card-content">
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Active Requests</span>
                <span className="gw-table-numeric" data-testid="telemetry-active-requests">
                  {data.active_requests}
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Worker Queue Depth</span>
                <span className="gw-table-numeric" data-testid="telemetry-queue-depth">
                  {data.telemetry.queue_depth}
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Queue Capacity</span>
                <span className="gw-table-numeric" data-testid="telemetry-queue-capacity">
                  {data.telemetry.queue_capacity}
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Dropped Records</span>
                <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                  <span className="gw-table-numeric" data-testid="telemetry-dropped-records">
                    {data.telemetry.dropped_records}
                  </span>
                  {data.telemetry.dropped_records > 0 && (
                    <Badge variant="warning" size="sm">Queue Drops</Badge>
                  )}
                </div>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Queue Pressure</span>
                {isTelemetrySaturated ? (
                  <Badge variant="warning" dot data-testid="queue-pressure-badge">High Pressure</Badge>
                ) : (
                  <Badge variant="success" dot data-testid="queue-pressure-badge">Normal</Badge>
                )}
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Section 4: Storage & SQLite State */}
        <Card data-testid="storage-card">
          <CardHeader>
            <div className="gw-system-card-title">
              <Database size={18} style={{ color: "var(--metric-tokens-cache)" }} aria-hidden="true" />
              <CardTitle>Storage &amp; SQLite</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="gw-system-card-content">
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Storage Status</span>
                {isStorageUnavailable ? (
                  <StatusPill label="Unavailable" variant="danger" />
                ) : isStorageDegraded ? (
                  <StatusPill label="Degraded" variant="warning" />
                ) : (
                  <StatusPill label="Healthy" variant="success" />
                )}
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">SQLite Schema Version</span>
                <span data-testid="storage-schema-version">
                  {data.storage.schema_version !== null ? (
                    <code className="gw-code-id">v{data.storage.schema_version}</code>
                  ) : (
                    <span className="gw-table-dimmed">Unavailable (null)</span>
                  )}
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Expected Schema</span>
                <code className="gw-code-id" data-testid="storage-current-schema-version">
                  v{data.storage.current_schema_version}
                </code>
              </div>
              {data.storage.schema_version !== null &&
                data.storage.schema_version !== data.storage.current_schema_version && (
                  <Alert variant="warning" title="Schema Version Mismatch">
                    SQLite schema is at version {data.storage.schema_version}, expected version {data.storage.current_schema_version}. Run gateway migrations.
                  </Alert>
                )}
            </div>
          </CardContent>
        </Card>

        {/* Section 5: Safety & Operational Limits */}
        <Card data-testid="limits-card">
          <CardHeader>
            <div className="gw-system-card-title">
              <Shield size={18} style={{ color: "var(--text-secondary)" }} aria-hidden="true" />
              <CardTitle>Operational Limits</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="gw-system-card-content">
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Request Metadata Retention</span>
                <span className="gw-system-v" data-testid="limit-request-retention">
                  {formatDurationSeconds(data.limits.request_retention_seconds)} ({data.limits.request_retention_seconds}s)
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Body Capture Retention</span>
                <span className="gw-system-v" data-testid="limit-body-retention">
                  {formatDurationSeconds(data.limits.body_retention_seconds)} ({data.limits.body_retention_seconds}s)
                </span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Max Captured Body Size</span>
                <span className="gw-system-v" data-testid="limit-body-bytes">
                  {data.limits.max_captured_body_bytes === 0 ? (
                    <span className="gw-table-dimmed">Capture Disabled (0 B)</span>
                  ) : (
                    `${formatByteCount(data.limits.max_captured_body_bytes)} (${data.limits.max_captured_body_bytes} B)`
                  )}
                </span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Diagnostics Summary Modal with Allowlisted Preview and Copy Action */}
      <Dialog
        isOpen={isDiagnosticsModalOpen}
        onClose={() => setIsDiagnosticsModalOpen(false)}
        title="Safe Diagnostics Summary"
        description="Allowlisted non-secret operational snapshot for troubleshooting and reports."
        size="lg"
        footer={
          <div style={{ display: "flex", justifyContent: "flex-end", gap: "0.75rem", width: "100%" }}>
            <Button
              variant="outline"
              onClick={() => setIsDiagnosticsModalOpen(false)}
            >
              Close
            </Button>
            <Button
              variant="primary"
              onClick={handleCopyDiagnostics}
              data-testid="copy-diagnostics-submit-btn"
            >
              {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
              <span>{copied ? "Copied!" : "Copy to Clipboard"}</span>
            </Button>
          </div>
        }
      >
        <div className="gw-system-preview-area">
          <p className="gw-system-preview-note">
            The preview below contains only strictly allowlisted, non-sensitive runtime metrics. No API keys, passwords, database contents, filesystem paths, hostnames, or environmental secrets are included.
          </p>
          <pre
            className="gw-system-preview-box"
            tabIndex={0}
            role="region"
            aria-label="Diagnostics snapshot preview"
            data-testid="diagnostics-preview-box"
          >
            {diagnosticsText}
          </pre>
        </div>
      </Dialog>
    </div>
  );
};

export default SystemPage;
