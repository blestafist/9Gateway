import React, { useState, useEffect, useMemo, useCallback } from "react";
import { useSearchParams, Link } from "react-router-dom";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { useAuth } from "../auth";
import {
  Button,
  Tabs,
  Alert,
  Skeleton,
} from "../../shared/ui";
import {
  formatTokenCount,
  formatCostMicros,
  formatTimestamp,
} from "../../shared/formatters";
import { AdminApiError, OfflineError } from "../../shared/transport";
import { OverviewPeriodPreset } from "./types";
import { overviewQueryKeys } from "./queryKeys";
import { getOverview } from "./api";
import { calculateDelta } from "./deltas";
import { MetricCard } from "./components/MetricCard";
import { HealthStatusStrip } from "./components/HealthStatusStrip";
import { KeySummaryCard } from "./components/KeySummaryCard";
import { RecentRequestsCard } from "./components/RecentRequestsCard";
import { UsageLinkCard } from "./components/UsageLinkCard";
import { RefreshCw, WifiOff, AlertTriangle, LogIn } from "lucide-react";
import "./overview.css";

const PERIOD_ITEMS = [
  { id: "1h", label: "1h" },
  { id: "24h", label: "24h" },
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
];

function getPeriodDurationHours(period: OverviewPeriodPreset): number {
  switch (period) {
    case "1h":
      return 1;
    case "7d":
      return 168;
    case "30d":
      return 720;
    case "24h":
    default:
      return 24;
  }
}

export const OverviewPage: React.FC = () => {
  const { isAuthenticated } = useAuth();
  const [searchParams, setSearchParams] = useSearchParams();

  const periodParam = searchParams.get("period") as OverviewPeriodPreset | null;
  const activePeriod: OverviewPeriodPreset =
    periodParam && ["1h", "24h", "7d", "30d"].includes(periodParam) ? periodParam : "24h";

  // Document visibility and online state tracking
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

  // Compute bounded timestamps for non-24h presets
  const computeBounds = useCallback((period: OverviewPeriodPreset) => {
    if (period === "24h") {
      return {};
    }
    const hours = getPeriodDurationHours(period);
    const now = new Date();
    const after = new Date(now.getTime() - hours * 3600 * 1000);
    return {
      before: now.toISOString(),
      after: after.toISOString(),
    };
  }, []);

  const [rangeSnapshot, setRangeSnapshot] = useState<{ after?: string; before?: string }>(() =>
    computeBounds(activePeriod)
  );

  useEffect(() => {
    setRangeSnapshot(computeBounds(activePeriod));
  }, [activePeriod, computeBounds]);

  const handlePeriodChange = (newPeriod: string) => {
    const nextPreset = newPeriod as OverviewPeriodPreset;
    setRangeSnapshot(computeBounds(nextPreset));
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (nextPreset === "24h") {
        next.delete("period");
      } else {
        next.set("period", nextPreset);
      }
      return next;
    });
  };

  // Only poll when mounted, online, authenticated, and document visible. >= 30s interval.
  const shouldPoll = isAuthenticated && isOnline && isVisible;

  const query = useQuery({
    queryKey: overviewQueryKeys.period(activePeriod, rangeSnapshot.after, rangeSnapshot.before),
    queryFn: ({ signal }) =>
      getOverview({ after: rangeSnapshot.after, before: rangeSnapshot.before }, signal),
    enabled: isAuthenticated,
    refetchInterval: shouldPoll ? 30_000 : false,
    refetchIntervalInBackground: false,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });

  const handleManualRefresh = () => {
    if (query.isFetching) return;
    if (activePeriod !== "24h") {
      setRangeSnapshot(computeBounds(activePeriod));
    }
    query.refetch();
  };

  const freshnessInfo = useMemo(() => {
    if (!isOnline) {
      return {
        text: "Offline — automatic polling paused",
        className: "gw-freshness-text--offline",
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
        className: "gw-freshness-text--stale",
      };
    }
    if (query.isError && query.data) {
      return {
        text: "Stale snapshot — refresh failed",
        className: "gw-freshness-text--stale",
      };
    }
    if (query.data?.data_timestamp) {
      return {
        text: `Updated at ${formatTimestamp(query.data.data_timestamp)}`,
        className: "",
      };
    }
    return {
      text: "Loading…",
      className: "",
    };
  }, [isOnline, isVisible, query.isFetching, query.isError, query.data]);

  // Full error state (no prior data to preserve)
  if (query.isError && !query.data) {
    const error = query.error;
    const isAuthError = error instanceof AdminApiError && error.isAuthError;
    const isCapacityExceeded = error instanceof AdminApiError && error.status === 503;
    const isOfflineErr = !isOnline || error instanceof OfflineError;

    if (isAuthError) {
      return (
        <div className="gw-page-content gw-overview-container" data-testid="overview-page">
          <Alert
            variant="danger"
            title="Session Expired"
            icon={<LogIn size={20} />}
            action={
              <Link to="/ui/login" className="gw-btn gw-btn--primary gw-btn--sm">
                Log In
              </Link>
            }
          >
            Your administrative session has expired or is invalid. Please log in again to continue viewing
            gateway telemetry.
          </Alert>
        </div>
      );
    }

    if (isCapacityExceeded) {
      return (
        <div className="gw-page-content gw-overview-container" data-testid="overview-page">
          <Alert
            variant="warning"
            title="Analytics Engine Capacity Constrained"
            icon={<AlertTriangle size={20} />}
            action={
              <Button
                variant="secondary"
                size="sm"
                onClick={handleManualRefresh}
                disabled={query.isFetching}
              >
                Retry
              </Button>
            }
          >
            The gateway is currently handling peak concurrent aggregation requests across proxy instances.
            Please retry in a moment.
          </Alert>
        </div>
      );
    }

    if (isOfflineErr) {
      return (
        <div className="gw-page-content gw-overview-container" data-testid="overview-page">
          <Alert
            variant="warning"
            title="Offline"
            icon={<WifiOff size={20} />}
            action={
              <Button
                variant="secondary"
                size="sm"
                onClick={handleManualRefresh}
                disabled={query.isFetching}
              >
                Retry
              </Button>
            }
          >
            Cannot connect to the gateway while offline. Reconnect to your network to view operational metrics.
          </Alert>
        </div>
      );
    }

    return (
      <div className="gw-page-content gw-overview-container" data-testid="overview-page">
        <Alert
          variant="danger"
          title="Failed to Load Overview Data"
          icon={<AlertTriangle size={20} />}
          action={
            <Button
              variant="secondary"
              size="sm"
              onClick={handleManualRefresh}
              disabled={query.isFetching}
            >
              Retry
            </Button>
          }
        >
          {error.message || "An unexpected error occurred while loading gateway telemetry."}
        </Alert>
      </div>
    );
  }

  // Initial loading state (no snapshot available yet)
  if (!query.data) {
    return (
      <div className="gw-page-content gw-overview-container" data-testid="overview-page">
        <div className="gw-overview-controls-bar">
          <div className="gw-overview-controls-left">
            <span className="gw-control-label">Period:</span>
            <Tabs
              items={PERIOD_ITEMS}
              activeTab={activePeriod}
              onChange={handlePeriodChange}
              aria-label="Overview time period"
            />
          </div>
        </div>

        <div className="gw-skeleton-kpi-grid" data-testid="overview-loading-skeletons">
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
          <Skeleton variant="rect" height={130} />
        </div>

        <Skeleton variant="rect" height={160} />
        <div className="gw-overview-columns">
          <Skeleton variant="rect" height={220} />
          <Skeleton variant="rect" height={220} />
        </div>
      </div>
    );
  }

  const data = query.data!;
  const current = data.current;
  const previous = data.previous;

  // Compute previous-period deltas
  const requestsDelta = calculateDelta(current.total_requests, previous.total_requests);
  const inputTokensDelta = calculateDelta(current.input_tokens, previous.input_tokens);
  const cachedTokensDelta = calculateDelta(current.cached_input_tokens, previous.cached_input_tokens);
  const outputTokensDelta = calculateDelta(current.output_tokens, previous.output_tokens);
  const costDelta = calculateDelta(current.cost_micros, previous.cost_micros, { isCost: true });

  const currentErrorsAndRejections = current.error_requests + current.rejected_requests;
  const previousErrorsAndRejections = previous.error_requests + previous.rejected_requests;
  const errorsDelta = calculateDelta(currentErrorsAndRejections, previousErrorsAndRejections, {
    isErrorMetric: true,
  });

  // Calculate prompt cache efficiency
  let cacheSubtext = "Cache prompt savings";
  if (current.input_tokens && current.cached_input_tokens) {
    const totalTokensPrompt = current.input_tokens + current.cached_input_tokens;
    if (totalTokensPrompt > 0) {
      const efficiency = ((current.cached_input_tokens / totalTokensPrompt) * 100).toFixed(1);
      cacheSubtext = `${efficiency}% prompt cache efficiency`;
    }
  }

  return (
    <div className="gw-page-content gw-overview-container" data-testid="overview-page">
      {/* Top Controls: Period Selector & Textual Freshness & Refresh */}
      <div className="gw-overview-controls-bar">
        <div className="gw-overview-controls-left">
          <span className="gw-control-label">Period:</span>
          <Tabs
            items={PERIOD_ITEMS}
            activeTab={activePeriod}
            onChange={handlePeriodChange}
            aria-label="Overview time period"
          />
        </div>

        <div className="gw-overview-controls-right">
          <div className="gw-freshness-strip" data-testid="freshness-indicator">
            <span className={`gw-freshness-text ${freshnessInfo.className}`}>
              {freshnessInfo.text}
            </span>
          </div>

          <Button
            variant="outline"
            size="sm"
            onClick={handleManualRefresh}
            disabled={query.isFetching}
            aria-busy={query.isFetching}
            aria-label="Refresh operational metrics"
            data-testid="overview-refresh-btn"
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

      {/* Stale Warning Banner if background refresh failed */}
      {query.isError && query.data && (
        <Alert
          variant="warning"
          title="Background Refresh Failed"
          icon={<AlertTriangle size={16} />}
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
          {query.error.message || "Failed to fetch latest metrics."} Displaying previous operational snapshot.
        </Alert>
      )}

      {/* Health & Operations Status Strip */}
      <HealthStatusStrip overview={data} />

      {/* KPI Metric Cards Grid */}
      <section aria-label="Operational Metrics" className="gw-kpi-grid">
        <MetricCard
          title="Total Requests"
          value={current.total_requests.toLocaleString()}
          valueColorVar="var(--metric-requests)"
          badgeText={activePeriod}
          badgeVariant="neutral"
          delta={requestsDelta}
          subtext={`${current.successful_requests.toLocaleString()} success · ${current.error_requests.toLocaleString()} errors · ${current.rejected_requests.toLocaleString()} rejected`}
          testId="kpi-total-requests"
        />

        <MetricCard
          title="Active Work"
          value={data.active_requests.toLocaleString()}
          valueColorVar="var(--accent-primary)"
          badgeText={data.active_requests > 0 ? "In-Flight" : "Idle"}
          badgeVariant={data.active_requests > 0 ? "warning" : "neutral"}
          subtext={
            data.active_requests === 0
              ? "No active in-flight requests"
              : `${data.active_requests} request${data.active_requests === 1 ? "" : "s"} currently proxying`
          }
          testId="kpi-active-requests"
        />

        <MetricCard
          title="Input Tokens"
          value={current.input_tokens !== null ? formatTokenCount(current.input_tokens) : "—"}
          valueColorVar="var(--metric-tokens-in)"
          badgeText="Prompt"
          badgeVariant="warning"
          delta={inputTokensDelta}
          subtext="Prompt tokens processed"
          testId="kpi-input-tokens"
        />

        <MetricCard
          title="Cached Tokens"
          value={
            current.cached_input_tokens !== null
              ? formatTokenCount(current.cached_input_tokens)
              : "—"
          }
          valueColorVar="var(--metric-tokens-cache)"
          badgeText="Cache"
          badgeVariant="info"
          delta={cachedTokensDelta}
          subtext={cacheSubtext}
          testId="kpi-cached-tokens"
        />

        <MetricCard
          title="Output Tokens"
          value={current.output_tokens !== null ? formatTokenCount(current.output_tokens) : "—"}
          valueColorVar="var(--metric-tokens-out)"
          badgeText="Generated"
          badgeVariant="success"
          delta={outputTokensDelta}
          subtext="Completed generation tokens"
          testId="kpi-output-tokens"
        />

        <MetricCard
          title="Est. Cost"
          value={current.cost_micros !== null ? formatCostMicros(current.cost_micros) : "—"}
          valueColorVar="var(--metric-cost)"
          badgeText="Est."
          badgeVariant="warning"
          delta={costDelta}
          subtext="Estimated provider billing"
          testId="kpi-estimated-cost"
        />

        <MetricCard
          title="Errors & Rejections"
          value={currentErrorsAndRejections.toLocaleString()}
          valueColorVar={
            currentErrorsAndRejections > 0 ? "var(--status-danger)" : "var(--text-primary)"
          }
          badgeText={currentErrorsAndRejections > 0 ? "Attention" : "Clean"}
          badgeVariant={currentErrorsAndRejections > 0 ? "danger" : "neutral"}
          delta={errorsDelta}
          subtext={`${current.error_requests} errors · ${current.rejected_requests} rejected`}
          testId="kpi-errors-rejections"
        />
      </section>

      {/* Deeper Operations Links & Recent Requests Section */}
      <div className="gw-overview-columns">
        <div className="gw-overview-sidebar-stack">
          <KeySummaryCard keyCounts={data.key_counts} />
          <UsageLinkCard />
        </div>

        <RecentRequestsCard requests={data.recent_requests} />
      </div>
    </div>
  );
};

export default OverviewPage;
