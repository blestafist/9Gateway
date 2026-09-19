import React, { useState, useCallback, useEffect, useMemo } from "react";
import { useNavigate, useParams, useSearchParams, Link } from "react-router-dom";
import { useQuery, useQueryClient, keepPreviousData, onlineManager } from "@tanstack/react-query";
import { KeyRound, Copy, Check, Plus, AlertCircle, WifiOff } from "lucide-react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
  IconButton,
  Badge,
  Alert,
  EmptyState,
  Skeleton,
} from "../../shared/ui";
import { useCursorPagination } from "../../shared/pagination";
import { listKeys } from "./api";
import { keyQueryKeys } from "./queryKeys";
import { KeyPageFilters } from "./types";
import { filterKeysOnPage } from "./helpers";
import { KeyFilterBar } from "./components/KeyFilterBar";
import { KeyTable } from "./components/KeyTable";
import { KeyCardList } from "./components/KeyCardList";
import { KeyPagination } from "./components/KeyPagination";
import { KeyDetailDrawer } from "./components/KeyDetailDrawer";
import { CreateKeyDialog } from "./components/CreateKeyDialog";
import "./keys.css";

const DEFAULT_PAGE_SIZE = 25;

export const KeysPage: React.FC = () => {
  const navigate = useNavigate();
  const { id: routeKeyId } = useParams<{ id?: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const queryClient = useQueryClient();

  // Create key dialog state
  const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);

  // Active key ID for deep link detail drawer
  const keyIdFromQuery = searchParams.get("keyId");
  const activeKeyId = routeKeyId || keyIdFromQuery || null;

  // Endpoint copy state
  const [copiedEndpoint, setCopiedEndpoint] = useState(false);
  const endpointUrl = typeof window !== "undefined"
    ? `${window.location.origin}/v1`
    : "http://localhost:8080/v1";

  const handleCopyEndpoint = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(endpointUrl);
      setCopiedEndpoint(true);
      setTimeout(() => setCopiedEndpoint(false), 2000);
    }
  };

  // Page size state (bounded max 100)
  const [pageSize, setPageSize] = useState<number>(DEFAULT_PAGE_SIZE);

  // Online status tracking
  const [isOnline, setIsOnline] = useState<boolean>(() => onlineManager.isOnline());
  useEffect(() => {
    return onlineManager.subscribe((status) => {
      setIsOnline(status);
    });
  }, []);

  // Client-side search and filters (evaluated only on loaded current page)
  const [filters, setFilters] = useState<KeyPageFilters>({
    searchQuery: "",
    status: "all",
    policy: "all",
  });

  const handleFilterChange = useCallback((updated: Partial<KeyPageFilters>) => {
    setFilters((prev) => ({ ...prev, ...updated }));
  }, []);

  const handleResetFilters = useCallback(() => {
    setFilters({
      searchQuery: "",
      status: "all",
      policy: "all",
    });
  }, []);

  // Pagination state via useCursorPagination
  // Cursor is strictly maintained in component memory, NEVER leaked into URL
  const {
    currentCursor,
    page,
    hasPrevPage,
    goToNextPage,
    goToPrevPage,
    resetPagination,
  } = useCursorPagination({
    filterKey: String(pageSize),
  });

  // Query current page of keys
  const {
    data,
    isLoading,
    isFetching,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: keyQueryKeys.list({ limit: pageSize, cursor: currentCursor }),
    queryFn: ({ signal }) => listKeys({ limit: pageSize, cursor: currentCursor }, signal),
    placeholderData: keepPreviousData,
  });

  const hasNextPage = Boolean(data?.next_cursor);
  const handleNextPage = useCallback(() => {
    if (data?.next_cursor) {
      goToNextPage(data.next_cursor);
    }
  }, [data?.next_cursor, goToNextPage]);

  // Prefetch at most one next page when next_cursor is returned
  useEffect(() => {
    if (data?.next_cursor) {
      void queryClient.prefetchQuery({
        queryKey: keyQueryKeys.list({ limit: pageSize, cursor: data.next_cursor }),
        queryFn: ({ signal }) =>
          listKeys({ limit: pageSize, cursor: data.next_cursor }, signal),
      });
    }
  }, [data?.next_cursor, pageSize, queryClient]);

  // Handle page size change: reset pagination back to page 1
  const handlePageSizeChange = useCallback((newSize: number) => {
    setPageSize(Math.min(100, Math.max(1, newSize)));
    resetPagination();
  }, [resetPagination]);

  // Current page loaded keys
  const loadedKeys = useMemo(() => data?.keys || [], [data?.keys]);

  // Filtered keys on current page
  const filteredKeys = useMemo(() => {
    return filterKeysOnPage(loadedKeys, filters);
  }, [loadedKeys, filters]);

  // Drawer handlers
  const handleSelectKey = useCallback((id: string) => {
    navigate(`/keys/${encodeURIComponent(id)}`);
  }, [navigate]);

  const handleCloseDrawer = useCallback(() => {
    const closingKeyId = activeKeyId;
    if (routeKeyId) {
      navigate("/keys");
    } else if (keyIdFromQuery) {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        next.delete("keyId");
        return next;
      });
    }
    if (closingKeyId) {
      requestAnimationFrame(() => {
        const targetEl =
          document.querySelector<HTMLElement>(`[data-testid="key-row-${closingKeyId}"]`) ||
          document.querySelector<HTMLElement>(`[data-testid="key-card-${closingKeyId}"]`);
        if (targetEl) {
          targetEl.focus();
        }
      });
    }
  }, [navigate, routeKeyId, keyIdFromQuery, setSearchParams, activeKeyId]);

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

  return (
    <div className="gw-page-content gw-keys-page-container" data-testid="keys-page">
      {/* Offline Alert */}
      {!isOnline && (
        <Alert
          variant="warning"
          icon={<WifiOff size={18} />}
          title="Network Connection Offline"
          data-testid="keys-offline-alert"
        >
          You are currently offline. Displayed API key metadata may be outdated.
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
          data-testid="keys-401-alert"
        >
          Your administrative session has expired or is invalid. Please sign in again to manage API keys.
        </Alert>
      )}

      {/* Invalid Cursor Alert */}
      {isInvalidCursor && (
        <Alert
          variant="warning"
          icon={<AlertCircle size={18} />}
          title="Invalid Pagination Cursor"
          action={
            <Button size="sm" variant="secondary" onClick={resetPagination}>
              Return to First Page
            </Button>
          }
          data-testid="keys-invalid-cursor-alert"
        >
          The pagination cursor is invalid or expired. Reset to the first page to resume browsing keys.
        </Alert>
      )}

      {/* General Error Alert */}
      {isError && !is401Error && !isInvalidCursor && (
        <Alert
          variant="danger"
          icon={<AlertCircle size={18} />}
          title="Failed to Load API Keys"
          action={
            <Button size="sm" variant="secondary" onClick={() => void refetch()}>
              Retry
            </Button>
          }
          data-testid="keys-error-alert"
        >
          Unable to retrieve API keys from the gateway admin service.
        </Alert>
      )}

      {/* Gateway API Endpoint Card */}
      <Card className="gw-endpoint-card">
        <CardHeader>
          <div className="gw-endpoint-header">
            <KeyRound size={18} style={{ color: "var(--accent-primary)" }} aria-hidden="true" />
            <CardTitle>API Endpoint</CardTitle>
          </div>
        </CardHeader>
        <CardContent>
          <div className="gw-endpoint-row">
            <span className="gw-endpoint-label">Local</span>
            <code className="gw-endpoint-url">{endpointUrl}</code>
            <IconButton
              icon={copiedEndpoint ? <Check size={16} /> : <Copy size={16} />}
              aria-label={copiedEndpoint ? "Copied endpoint URL" : "Copy endpoint URL"}
              variant="secondary"
              size="sm"
              onClick={handleCopyEndpoint}
            />
          </div>
        </CardContent>
      </Card>

      {/* Main Keys Card */}
      <Card>
        <CardHeader>
          <div className="gw-keys-header-row">
            <div className="gw-keys-title-group">
              <div className="gw-keys-title-line">
                <CardTitle>API Keys</CardTitle>
                <Badge variant="neutral" size="sm">
                  {loadedKeys.length} {loadedKeys.length === 1 ? "key" : "keys"}
                </Badge>
              </div>
              <p className="gw-card-subtitle">
                Inspect configured access keys, display prefixes, and security policies. Full keys are never displayed.
              </p>
            </div>

            {/* Create Key Button */}
            <Button
              variant="primary"
              size="sm"
              leftIcon={<Plus size={16} aria-hidden="true" />}
              onClick={() => setIsCreateDialogOpen(true)}
              data-testid="open-create-key-btn"
            >
              Create Key
            </Button>
          </div>
        </CardHeader>

        <CardContent>
          {/* Client-Side Search and Filtering Controls */}
          <KeyFilterBar
            filters={filters}
            onFilterChange={handleFilterChange}
            onResetFilters={handleResetFilters}
            pageSize={pageSize}
            onPageSizeChange={handlePageSizeChange}
            filteredCount={filteredKeys.length}
            totalCount={loadedKeys.length}
          />

          {/* Initial Loading Skeleton */}
          {isLoading && !data && (
            <div data-testid="keys-loading-skeleton" style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
              <Skeleton height="44px" />
              <Skeleton height="44px" />
              <Skeleton height="44px" />
            </div>
          )}

          {/* Empty State: No keys returned from server on this page */}
          {!isLoading && loadedKeys.length === 0 && (
            <EmptyState
              title="No API Keys Found"
              description="No gateway API keys have been created yet. Create a key using the admin CLI or API to begin routing traffic."
              data-testid="keys-empty-state"
            />
          )}

          {/* Empty Filter State: Keys exist on page, but filters match none */}
          {!isLoading && loadedKeys.length > 0 && filteredKeys.length === 0 && (
            <EmptyState
              title="No Matching Keys on This Page"
              description="No keys on the current page match your active search and filter criteria. Note that filtering operates on the loaded page only."
              action={
                <Button variant="secondary" size="sm" onClick={handleResetFilters}>
                  Clear Filters
                </Button>
              }
              data-testid="keys-empty-filter-state"
            />
          )}

          {/* Desktop Table View */}
          {filteredKeys.length > 0 && (
            <KeyTable keys={filteredKeys} onSelectKey={handleSelectKey} />
          )}

          {/* Mobile Deliberate Cards View */}
          {filteredKeys.length > 0 && (
            <KeyCardList keys={filteredKeys} onSelectKey={handleSelectKey} />
          )}

          {/* Cursor Pagination Bar */}
          {!isLoading && loadedKeys.length > 0 && (
            <KeyPagination
              page={page}
              hasNextPage={hasNextPage}
              hasPrevPage={hasPrevPage}
              onNextPage={handleNextPage}
              onPrevPage={goToPrevPage}
              isFetching={isFetching}
              itemCount={filteredKeys.length}
            />
          )}
        </CardContent>
      </Card>

      {/* Key Detail Drawer Shell */}
      <KeyDetailDrawer
        keyId={activeKeyId}
        isOpen={Boolean(activeKeyId)}
        onClose={handleCloseDrawer}
      />

      {/* Create Key Modal Dialog */}
      <CreateKeyDialog
        isOpen={isCreateDialogOpen}
        onClose={() => setIsCreateDialogOpen(false)}
      />
    </div>
  );
};

export default KeysPage;
