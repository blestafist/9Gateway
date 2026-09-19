import React, { useState, lazy, Suspense, useMemo } from "react";
import { Link, useLocation } from "react-router-dom";
import { useQuery, onlineManager } from "@tanstack/react-query";
import {
  ArrowLeft,
  AlertCircle,
  WifiOff,
  FileCode2,
  Download,
  Eye,
} from "lucide-react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
  Badge,
  Alert,
  EmptyState,
  Skeleton,
} from "../../../shared/ui";
import { getRequestDetail, getRequestBody } from "../api";
import { requestQueryKeys } from "../queryKeys";
import { RequestBodyKind } from "../types";
import { formatBodyKindLabel, downloadBodyBytes, truncateId } from "../helpers";
import { RequestMetadataSection } from "./RequestMetadataSection";
import { RequestTimeline } from "./RequestTimeline";

// Lazy-load BodyViewer only when explicitly opened by the operator
const LazyBodyViewer = lazy(() => import("./BodyViewer"));

export interface RequestDetailViewProps {
  requestId: string;
}

export const RequestDetailView: React.FC<RequestDetailViewProps> = ({
  requestId,
}) => {
  const location = useLocation();

  // Preserved history return link
  const backUrl = useMemo(() => {
    const search = location.search;
    return search ? `/requests${search}` : "/requests";
  }, [location.search]);

  // Online status tracking
  const [isOnline, setIsOnline] = useState<boolean>(() => onlineManager.isOnline());
  React.useEffect(() => {
    return onlineManager.subscribe((status) => {
      setIsOnline(status);
    });
  }, []);

  // Fetch request detail
  const {
    data: detail,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: requestQueryKeys.detail(requestId),
    queryFn: ({ signal }) => getRequestDetail(requestId, signal),
    retry: (failureCount, err) => {
      if (
        err &&
        typeof err === "object" &&
        "status" in err &&
        ((err as { status?: number }).status === 404 ||
          (err as { status?: number }).status === 400 ||
          (err as { status?: number }).status === 401)
      ) {
        return false;
      }
      return failureCount < 2;
    },
  });

  // Body viewer lazy-load toggle & selected kind
  const [activeBodyKind, setActiveBodyKind] = useState<RequestBodyKind | null>(null);
  const [isBodyViewerOpen, setIsBodyViewerOpen] = useState(false);

  // Direct download state without opening viewer
  const [downloadingKind, setDownloadingKind] = useState<RequestBodyKind | null>(null);

  const handleOpenBodyViewer = (kind: RequestBodyKind) => {
    setActiveBodyKind(kind);
    setIsBodyViewerOpen(true);
  };

  const handleCloseBodyViewer = () => {
    setIsBodyViewerOpen(false);
    setActiveBodyKind(null);
  };

  const handleDirectDownload = async (kind: RequestBodyKind) => {
    setDownloadingKind(kind);
    try {
      const content = await getRequestBody(requestId, kind);
      const rawBytes = content.bytes ?? new TextEncoder().encode(content.data);
      const ext = content.content_type.includes("json") ? "json" : "bin";
      const filename = `request-${truncateId(requestId)}-${kind}.${ext}`;
      downloadBodyBytes(rawBytes, filename, content.content_type);
    } catch {
      // Direct download error handling is caught gracefully
    } finally {
      setDownloadingKind(null);
    }
  };

  // Status checks
  const is401Error =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    (error as { status?: number }).status === 401;

  const is404Error =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    (error as { status?: number }).status === 404;

  const is400Error =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    (error as { status?: number }).status === 400;

  return (
    <div
      className="gw-page-content gw-request-detail-container"
      data-testid="request-detail-view"
    >
      {/* Return Link preserving history filters */}
      <div className="gw-detail-nav-bar">
        <Link
          to={backUrl}
          className="gw-back-link"
          data-testid="detail-back-link"
        >
          <ArrowLeft size={16} aria-hidden="true" />
          <span>Back to Requests</span>
        </Link>
      </div>

      {/* Offline Alert */}
      {!isOnline && (
        <Alert
          variant="warning"
          icon={<WifiOff size={18} />}
          title="Network Connection Offline"
          data-testid="detail-offline-alert"
        >
          You are currently offline. Request metadata may be outdated.
        </Alert>
      )}

      {/* 401 Session Expired */}
      {is401Error && (
        <Alert
          variant="danger"
          icon={<AlertCircle size={18} />}
          title="Session Expired"
          action={
            <Link to="/login" style={{ textDecoration: "none" }}>
              <Button size="sm" variant="secondary">
                Sign In
              </Button>
            </Link>
          }
          data-testid="detail-401-alert"
        >
          Your administrative session has expired. Please sign in again to view request details.
        </Alert>
      )}

      {/* 404 Not Found */}
      {is404Error && (
        <EmptyState
          title="Request Not Found"
          description="This request record was not found. It may have expired and been removed by history retention policy, or the ID may be incorrect."
          action={
            <Link to={backUrl} style={{ textDecoration: "none" }}>
              <Button size="sm" variant="secondary">
                Return to Requests
              </Button>
            </Link>
          }
          data-testid="detail-404-state"
        />
      )}

      {/* 400 Invalid ID */}
      {is400Error && (
        <Alert
          variant="danger"
          icon={<AlertCircle size={18} />}
          title="Invalid Request ID"
          action={
            <Link to={backUrl} style={{ textDecoration: "none" }}>
              <Button size="sm" variant="secondary">
                Return to Requests
              </Button>
            </Link>
          }
          data-testid="detail-400-alert"
        >
          The requested identifier is not a valid request ID.
        </Alert>
      )}

      {/* General 500 / Fetch Error */}
      {isError && !is401Error && !is404Error && !is400Error && (
        <Alert
          variant="danger"
          icon={<AlertCircle size={18} />}
          title="Failed to Load Request Details"
          action={
            <Button size="sm" variant="secondary" onClick={() => void refetch()}>
              Retry
            </Button>
          }
          data-testid="detail-error-alert"
        >
          Unable to retrieve request details from the gateway admin service.
        </Alert>
      )}

      {/* Loading Skeleton */}
      {isLoading && (
        <div
          className="gw-detail-loading-skeleton"
          data-testid="detail-loading-skeleton"
        >
          <Skeleton height="32px" />
          <Skeleton height="140px" />
          <Skeleton height="140px" />
          <Skeleton height="200px" />
        </div>
      )}

      {/* Content when loaded */}
      {!isLoading && detail && (
        <div className="gw-detail-body-stack">
          {/* Metadata Cards */}
          <RequestMetadataSection detail={detail} />

          {/* Lifecycle Timeline */}
          <RequestTimeline detail={detail} />

          {/* Captured Bodies Section */}
          <Card className="gw-detail-bodies-card" data-testid="detail-bodies-card">
            <CardHeader>
              <div className="gw-bodies-header-line">
                <div className="gw-bodies-header-title">
                  <FileCode2 size={16} aria-hidden="true" />
                  <CardTitle>Captured Bodies</CardTitle>
                </div>
                <Badge variant="neutral" size="sm">
                  {detail.has_bodies.length}{" "}
                  {detail.has_bodies.length === 1 ? "captured" : "captured"}
                </Badge>
              </div>
            </CardHeader>

            <CardContent>
              {detail.has_bodies.length === 0 ? (
                <div
                  className="gw-no-bodies-notice gw-table-dimmed"
                  data-testid="no-bodies-notice"
                >
                  No payload bodies were captured for this request. Body logging was either
                  disabled by key policy or capture capacity was reached.
                </div>
              ) : (
                <div className="gw-available-bodies-list" data-testid="available-bodies-list">
                  {detail.has_bodies.map((kind) => {
                    const isDownloading = downloadingKind === kind;

                    return (
                      <div
                        key={kind}
                        className="gw-available-body-item"
                        data-testid={`available-body-${kind}`}
                      >
                        <div className="gw-available-body-info">
                          <span className="gw-available-body-name">
                            {formatBodyKindLabel(kind)}
                          </span>
                          <span className="gw-table-dimmed gw-available-body-desc">
                            {kind === "client_request" && "Client payload sent to gateway"}
                            {kind === "upstream_request" && "Dispatched payload sent to upstream"}
                            {kind === "response" && "Response payload received from upstream"}
                          </span>
                        </div>

                        <div className="gw-available-body-actions">
                          <Button
                            variant="secondary"
                            size="sm"
                            leftIcon={<Eye size={13} />}
                            onClick={() => handleOpenBodyViewer(kind)}
                            data-testid={`open-body-${kind}-btn`}
                          >
                            View Body
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            leftIcon={<Download size={13} />}
                            onClick={() => void handleDirectDownload(kind)}
                            disabled={isDownloading}
                            data-testid={`download-body-${kind}-btn`}
                          >
                            {isDownloading ? "Downloading…" : "Download"}
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </CardContent>
          </Card>

          {/* Lazy-Loaded Safe Body Viewer */}
          {isBodyViewerOpen && activeBodyKind && (
            <Suspense
              fallback={
                <Card>
                  <CardContent>
                    <Skeleton height="150px" />
                  </CardContent>
                </Card>
              }
            >
              <LazyBodyViewer
                requestId={detail.request_id}
                availableKinds={detail.has_bodies}
                initialKind={activeBodyKind}
                onClose={handleCloseBodyViewer}
              />
            </Suspense>
          )}
        </div>
      )}
    </div>
  );
};

export default RequestDetailView;
