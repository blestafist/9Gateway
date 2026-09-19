import React, { useState, useCallback, useEffect, useMemo } from "react";
import { useNavigate, useSearchParams, Link } from "react-router-dom";
import { useQuery, useQueryClient, keepPreviousData, onlineManager } from "@tanstack/react-query";
import { AlertCircle, WifiOff } from "lucide-react";
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
} from "../../shared/ui";
import { useCursorPagination, sanitizeShareableParams } from "../../shared/pagination";
import { listKeys, keyQueryKeys } from "../keys";
import { listRequests } from "./api";
import { requestQueryKeys } from "./queryKeys";
import { RequestListFilters, RequestRangePreset } from "./types";
import { computeRequestPresetBounds, isValidIsoDate } from "./helpers";
import { RequestFilterBar } from "./components/RequestFilterBar";
import { RequestTable } from "./components/RequestTable";
import { RequestCardList } from "./components/RequestCardList";
import { RequestPagination } from "./components/RequestPagination";
import "./requests.css";

const DEFAULT_PAGE_SIZE = 25;

export const RequestsPage: React.FC = () => {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const queryClient = useQueryClient();

  // Strip opaque cursor if leaked in URL (e.g. from pasted link)
  useEffect(() => {
    if (searchParams.has("cursor") || searchParams.has("next_cursor")) {
      setSearchParams(
        (prev) => {
          return sanitizeShareableParams(prev);
        },
        { replace: true }
      );
    }
  }, [searchParams, setSearchParams]);

  // Online status tracking
  const [isOnline, setIsOnline] = useState<boolean>(() => onlineManager.isOnline());
  useEffect(() => {
    return onlineManager.subscribe((status) => {
      setIsOnline(status);
    });
  }, []);

  // Parse filters from URL searchParams
  const keyIdParam = searchParams.get("key_id") || searchParams.get("key") || "";
  const rangeParam = (searchParams.get("range") as RequestRangePreset) || "all";
  const afterParam = searchParams.get("after") || "";
  const beforeParam = searchParams.get("before") || "";
  const limitParam = parseInt(searchParams.get("limit") || String(DEFAULT_PAGE_SIZE), 10);

  // Validated page size capped at 100
  const validatedPageSize = useMemo(() => {
    if (Number.isNaN(limitParam) || limitParam <= 0) {
      return DEFAULT_PAGE_SIZE;
    }
    return Math.min(100, limitParam);
  }, [limitParam]);

  // State
  const [keyId, setKeyId] = useState<string>(keyIdParam);
  const [preset, setPreset] = useState<RequestRangePreset>(() => {
    if (afterParam || beforeParam || rangeParam === "custom") {
      return "custom";
    }
    if (["all", "1h", "24h", "7d", "30d"].includes(rangeParam)) {
      return rangeParam;
    }
    return "all";
  });
  const [customAfter, setCustomAfter] = useState<string>(afterParam);
  const [customBefore, setCustomBefore] = useState<string>(beforeParam);
  const [pageSize, setPageSize] = useState<number>(validatedPageSize);

  // Sync state if URL searchParams change externally
  useEffect(() => {
    setKeyId(keyIdParam);
  }, [keyIdParam]);

  useEffect(() => {
    if (afterParam || beforeParam || rangeParam === "custom") {
      setPreset("custom");
      setCustomAfter(afterParam);
      setCustomBefore(beforeParam);
    } else if (["all", "1h", "24h", "7d", "30d"].includes(rangeParam)) {
      setPreset(rangeParam);
      setCustomAfter("");
      setCustomBefore("");
    } else {
      setPreset("all");
      setCustomAfter("");
      setCustomBefore("");
    }
  }, [rangeParam, afterParam, beforeParam]);

  useEffect(() => {
    setPageSize(validatedPageSize);
  }, [validatedPageSize]);

  // Fetch bounded key list for selector
  const { data: keysData } = useQuery({
    queryKey: keyQueryKeys.list({ limit: 100 }),
    queryFn: ({ signal }) => listKeys({ limit: 100 }, signal),
  });
  const keys = useMemo(() => keysData?.keys || [], [keysData?.keys]);

  // Helper to update URL params cleanly
  const updateUrlParams = useCallback(
    (updates: {
      keyId?: string;
      preset?: RequestRangePreset;
      after?: string;
      before?: string;
      limit?: number;
    }) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete("cursor");
          next.delete("next_cursor");

          if (updates.keyId !== undefined) {
            if (updates.keyId) {
              next.set("key_id", updates.keyId);
            } else {
              next.delete("key_id");
              next.delete("key");
            }
          }

          if (updates.preset !== undefined) {
            if (updates.preset === "all") {
              next.delete("range");
              next.delete("after");
              next.delete("before");
            } else if (updates.preset === "custom") {
              next.set("range", "custom");
              if (updates.after) next.set("after", updates.after);
              if (updates.before) next.set("before", updates.before);
            } else {
              next.set("range", updates.preset);
              next.delete("after");
              next.delete("before");
            }
          }

          if (updates.after !== undefined && updates.preset === "custom") {
            if (updates.after) next.set("after", updates.after);
            else next.delete("after");
          }

          if (updates.before !== undefined && updates.preset === "custom") {
            if (updates.before) next.set("before", updates.before);
            else next.delete("before");
          }

          if (updates.limit !== undefined) {
            if (updates.limit !== DEFAULT_PAGE_SIZE) {
              next.set("limit", String(updates.limit));
            } else {
              next.delete("limit");
            }
          }

          return next;
        },
        { replace: false }
      );
    },
    [setSearchParams]
  );

  // Compute effective RFC3339 bounds for request query
  const effectiveBounds = useMemo(() => {
    if (preset === "custom") {
      return {
        after: isValidIsoDate(customAfter) ? new Date(customAfter).toISOString() : undefined,
        before: isValidIsoDate(customBefore) ? new Date(customBefore).toISOString() : undefined,
      };
    }
    return computeRequestPresetBounds(preset);
  }, [preset, customAfter, customBefore]);

  // Cursor pagination filter key resets page to 1 on filter change
  const filterKey = `${keyId}:${preset}:${effectiveBounds.after || ""}:${effectiveBounds.before || ""}:${pageSize}`;

  const {
    currentCursor,
    page,
    hasPrevPage,
    goToNextPage,
    goToPrevPage,
    resetPagination,
  } = useCursorPagination({
    filterKey,
  });

  // Query filters sent to backend
  const requestFilters = useMemo<RequestListFilters>(() => {
    return {
      limit: pageSize,
      cursor: currentCursor,
      key_id: keyId || undefined,
      after: effectiveBounds.after,
      before: effectiveBounds.before,
    };
  }, [pageSize, currentCursor, keyId, effectiveBounds.after, effectiveBounds.before]);

  // Query requests
  const {
    data,
    isLoading,
    isFetching,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: requestQueryKeys.list(requestFilters),
    queryFn: ({ signal }) => listRequests(requestFilters, signal),
    placeholderData: keepPreviousData,
  });

  const hasNextPage = Boolean(data?.next_cursor);
  const handleNextPage = useCallback(() => {
    if (data?.next_cursor) {
      goToNextPage(data.next_cursor);
    }
  }, [data?.next_cursor, goToNextPage]);

  // Prefetch single next page
  useEffect(() => {
    if (data?.next_cursor) {
      void queryClient.prefetchQuery({
        queryKey: requestQueryKeys.list({ ...requestFilters, cursor: data.next_cursor }),
        queryFn: ({ signal }) =>
          listRequests({ ...requestFilters, cursor: data.next_cursor }, signal),
      });
    }
  }, [data?.next_cursor, requestFilters, queryClient]);

  // Handlers for filter controls
  const handleKeyIdChange = useCallback(
    (newKeyId: string) => {
      setKeyId(newKeyId);
      updateUrlParams({ keyId: newKeyId });
      resetPagination();
    },
    [updateUrlParams, resetPagination]
  );

  const handlePresetChange = useCallback(
    (newPreset: RequestRangePreset) => {
      setPreset(newPreset);
      if (newPreset !== "custom") {
        setCustomAfter("");
        setCustomBefore("");
        updateUrlParams({ preset: newPreset, after: "", before: "" });
      } else {
        updateUrlParams({ preset: newPreset });
      }
      resetPagination();
    },
    [updateUrlParams, resetPagination]
  );

  const handleCustomRangeApply = useCallback(
    (after: string, before: string) => {
      setCustomAfter(after);
      setCustomBefore(before);
      setPreset("custom");
      updateUrlParams({ preset: "custom", after, before });
      resetPagination();
    },
    [updateUrlParams, resetPagination]
  );

  const handlePageSizeChange = useCallback(
    (newSize: number) => {
      const capped = Math.min(100, Math.max(1, newSize));
      setPageSize(capped);
      updateUrlParams({ limit: capped });
      resetPagination();
    },
    [updateUrlParams, resetPagination]
  );

  const handleResetFilters = useCallback(() => {
    setKeyId("");
    setPreset("all");
    setCustomAfter("");
    setCustomBefore("");
    updateUrlParams({ keyId: "", preset: "all", after: "", before: "" });
    resetPagination();
  }, [updateUrlParams, resetPagination]);

  const handleSelectRequest = useCallback(
    (requestId: string) => {
      navigate(`/requests/${encodeURIComponent(requestId)}`);
    },
    [navigate]
  );

  const loadedRequests = useMemo(() => data?.requests || [], [data?.requests]);

  // Error inspection
  const is401Error =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    error.status === 401;

  const isInvalidCursor =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    error.status === 400 &&
    Boolean(currentCursor);

  const isInvalidParams =
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    error.status === 400 &&
    !currentCursor;

  // Selected key name for contextual empty description
  const selectedKeyName = useMemo(() => {
    if (!keyId) return null;
    const found = keys.find((k) => k.id === keyId);
    return found ? found.name : keyId;
  }, [keyId, keys]);

  const hasActiveFilters = Boolean(keyId) || preset !== "all";

  return (
    <div
      className="gw-page-content gw-requests-page-container"
      data-testid="requests-page"
    >
      {/* Offline Alert */}
      {!isOnline && (
        <Alert
          variant="warning"
          icon={<WifiOff size={18} />}
          title="Network Connection Offline"
          data-testid="requests-offline-alert"
        >
          You are currently offline. Displayed request history may be outdated.
        </Alert>
      )}

      {/* 401 Session Expired Alert */}
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
          data-testid="requests-401-alert"
        >
          Your administrative session has expired or is invalid. Please sign in again to view request history.
        </Alert>
      )}

      {/* Invalid Cursor Alert */}
      {isInvalidCursor && (
        <Alert
          variant="warning"
          icon={<AlertCircle size={18} />}
          title="Invalid or Expired Cursor"
          action={
            <Button size="sm" variant="secondary" onClick={resetPagination}>
              Return to First Page
            </Button>
          }
          data-testid="requests-invalid-cursor-alert"
        >
          The pagination cursor is invalid or expired. Reset to the first page to resume browsing request history.
        </Alert>
      )}

      {/* Invalid Parameters Alert (e.g. malformed bookmarked URL) */}
      {isInvalidParams && (
        <Alert
          variant="danger"
          icon={<AlertCircle size={18} />}
          title="Invalid Request Parameters"
          action={
            <Button size="sm" variant="secondary" onClick={handleResetFilters}>
              Reset Filters
            </Button>
          }
          data-testid="requests-invalid-params-alert"
        >
          The request parameters or time bounds are invalid. Reset filters to return to safe defaults.
        </Alert>
      )}

      {/* General Server Error Alert */}
      {isError && !is401Error && !isInvalidCursor && !isInvalidParams && (
        <Alert
          variant="danger"
          icon={<AlertCircle size={18} />}
          title="Failed to Load Requests"
          action={
            <Button size="sm" variant="secondary" onClick={() => void refetch()}>
              Retry
            </Button>
          }
          data-testid="requests-error-alert"
        >
          Unable to retrieve request history from the gateway admin service.
        </Alert>
      )}

      {/* Main Request History Card */}
      <Card>
        <CardHeader>
          <div className="gw-requests-header-row">
            <div className="gw-requests-title-group">
              <div className="gw-requests-title-line">
                <CardTitle>Recent Request Traces</CardTitle>
                <Badge variant="neutral" size="sm">
                  {loadedRequests.length}{" "}
                  {loadedRequests.length === 1 ? "request" : "requests"}
                </Badge>
              </div>
              <p className="gw-card-subtitle">
                Inspect completed gateway requests, status codes, outcomes, and latency.
              </p>
            </div>
          </div>
        </CardHeader>

        <CardContent>
          {/* Filters Bar */}
          <RequestFilterBar
            keyId={keyId}
            onKeyIdChange={handleKeyIdChange}
            preset={preset}
            onPresetChange={handlePresetChange}
            customAfter={customAfter}
            customBefore={customBefore}
            onCustomRangeApply={handleCustomRangeApply}
            pageSize={pageSize}
            onPageSizeChange={handlePageSizeChange}
            keys={keys}
            isFetching={isFetching}
            onRefresh={() => void refetch()}
            onResetFilters={handleResetFilters}
          />

          {/* Loading Skeleton */}
          {isLoading && loadedRequests.length === 0 && (
            <div
              data-testid="requests-loading-skeleton"
              style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}
            >
              <Skeleton height="44px" />
              <Skeleton height="44px" />
              <Skeleton height="44px" />
            </div>
          )}

          {/* Contextual Empty State: Initial empty history */}
          {!isLoading && loadedRequests.length === 0 && !hasActiveFilters && (
            <EmptyState
              title="No Requests Recorded"
              description="No completed requests have been recorded yet. As traffic flows through the gateway, completed requests will appear here."
              data-testid="requests-empty-state"
            />
          )}

          {/* Contextual Empty State: Filtered by key */}
          {!isLoading && loadedRequests.length === 0 && keyId && (
            <EmptyState
              title="No Requests for Key"
              description={`No completed requests found for API key "${selectedKeyName}". The key may have no traffic recorded or may have been deleted.`}
              action={
                <Button size="sm" variant="secondary" onClick={handleResetFilters}>
                  Clear Filters
                </Button>
              }
              data-testid="requests-empty-filtered-key"
            />
          )}

          {/* Contextual Empty State: Filtered by range or other */}
          {!isLoading && loadedRequests.length === 0 && !keyId && hasActiveFilters && (
            <EmptyState
              title="No Matching Requests"
              description="No completed requests match the specified time range. Try selecting a wider time range or clearing the filter."
              action={
                <Button size="sm" variant="secondary" onClick={handleResetFilters}>
                  Clear Filters
                </Button>
              }
              data-testid="requests-empty-filtered-range"
            />
          )}

          {/* Data Views: Dense Desktop Table & Intentional Mobile Cards */}
          {loadedRequests.length > 0 && (
            <>
              <RequestTable
                requests={loadedRequests}
                onSelectRequest={handleSelectRequest}
              />
              <RequestCardList
                requests={loadedRequests}
                onSelectRequest={handleSelectRequest}
              />
              <RequestPagination
                page={page}
                hasNextPage={hasNextPage}
                hasPrevPage={hasPrevPage}
                onNextPage={handleNextPage}
                onPrevPage={goToPrevPage}
                isFetching={isFetching}
                itemCount={loadedRequests.length}
              />
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
};

export default RequestsPage;
