import React, { useState, useEffect } from "react";
import { Tabs, Select, Input, Button } from "../../../shared/ui";
import { AdminKeyListItem } from "../../keys";
import { RequestRangePreset } from "../types";
import { Calendar, ArrowRight, RefreshCw, X } from "lucide-react";
import { isValidCustomRequestRange, parseStrictUtcRfc3339 } from "../helpers";

export interface RequestFilterBarProps {
  keyId: string;
  onKeyIdChange: (keyId: string) => void;
  preset: RequestRangePreset;
  onPresetChange: (preset: RequestRangePreset) => void;
  customAfter?: string;
  customBefore?: string;
  onCustomRangeApply: (after: string, before: string) => void;
  pageSize: number;
  onPageSizeChange: (pageSize: number) => void;
  keys: AdminKeyListItem[];
  isFetching: boolean;
  onRefresh: () => void;
  onResetFilters: () => void;
}

const PRESET_TABS = [
  { id: "all", label: "All retained" },
  { id: "1h", label: "1h" },
  { id: "24h", label: "24h" },
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
  { id: "custom", label: "Custom UTC" },
];

const PAGE_SIZE_OPTIONS = [
  { value: "10", label: "10 / page" },
  { value: "25", label: "25 / page" },
  { value: "50", label: "50 / page" },
  { value: "100", label: "100 / page" },
];

export const RequestFilterBar: React.FC<RequestFilterBarProps> = ({
  keyId,
  onKeyIdChange,
  preset,
  onPresetChange,
  customAfter = "",
  customBefore = "",
  onCustomRangeApply,
  pageSize,
  onPageSizeChange,
  keys,
  isFetching,
  onRefresh,
  onResetFilters,
}) => {
  const [startInput, setStartInput] = useState(customAfter);
  const [endInput, setEndInput] = useState(customBefore);
  const [customError, setCustomError] = useState<string | null>(null);

  useEffect(() => {
    setStartInput(customAfter);
  }, [customAfter]);

  useEffect(() => {
    setEndInput(customBefore);
  }, [customBefore]);

  // Build bounded key options
  const keyOptions: Array<{ value: string; label: string }> = [
    { value: "", label: "All API Keys" },
  ];

  let foundSelectedKey = false;
  for (const k of keys) {
    if (k.id === keyId) {
      foundSelectedKey = true;
    }
    keyOptions.push({
      value: k.id,
      label: `${k.name} (${k.display_prefix})`,
    });
  }

  // If a keyId is selected but not present in loaded keys (e.g. unknown or deleted key bookmark)
  if (keyId && !foundSelectedKey) {
    keyOptions.push({
      value: keyId,
      label: `Unknown Key (${keyId})`,
    });
  }

  const handleApplyCustom = (e: React.FormEvent) => {
    e.preventDefault();
    setCustomError(null);

    const s = startInput.trim();
    const b = endInput.trim();

    if (!s || !b) {
      setCustomError("Both Start UTC and End UTC timestamps are required.");
      return;
    }

    const startDate = parseStrictUtcRfc3339(s);
    const endDate = parseStrictUtcRfc3339(b);
    if (!startDate) {
      setCustomError(
        "Invalid Start UTC timestamp. Expected strict RFC3339 UTC format (e.g. 2026-09-17T00:00:00Z)."
      );
      return;
    }
    if (!endDate) {
      setCustomError(
        "Invalid End UTC timestamp. Expected strict RFC3339 UTC format (e.g. 2026-09-18T00:00:00Z)."
      );
      return;
    }
    if (!isValidCustomRequestRange(s, b)) {
      setCustomError("Start UTC timestamp must be strictly before End UTC timestamp.");
      return;
    }

    // Keep the user's strict UTC values; the page normalizes them only after
    // both bounds have passed validation.
    onCustomRangeApply(s, b);
  };

  const hasActiveFilters = Boolean(keyId) || preset !== "all";

  return (
    <div className="gw-requests-filter-bar" data-testid="requests-filter-bar">
      <div className="gw-requests-filter-controls">
        {/* Presets Bar */}
        <div className="gw-requests-presets-group">
          <span className="gw-control-label">Range:</span>
          <Tabs
            items={PRESET_TABS}
            activeTab={preset}
            onChange={(tabId) => onPresetChange(tabId as RequestRangePreset)}
            aria-label="Time range presets"
          />
        </div>

        {/* Key Selector, Page Size, and Refresh */}
        <div className="gw-requests-filter-inputs">
          <div className="gw-requests-key-select">
            <span className="gw-control-label" style={{ marginRight: "6px" }}>
              Key:
            </span>
            <Select
              aria-label="Filter by API key"
              value={keyId}
              onChange={(e) => onKeyIdChange(e.target.value)}
              options={keyOptions}
              style={{ minWidth: "170px" }}
            />
          </div>

          <div className="gw-requests-pagesize-select">
            <span className="gw-control-label" style={{ marginRight: "6px" }}>
              Page:
            </span>
            <Select
              aria-label="Page size"
              value={String(pageSize)}
              onChange={(e) => {
                const parsed = parseInt(e.target.value, 10);
                if (!Number.isNaN(parsed)) {
                  onPageSizeChange(parsed);
                }
              }}
              options={PAGE_SIZE_OPTIONS}
              style={{ minWidth: "110px" }}
            />
          </div>

          <Button
            variant="outline"
            size="sm"
            onClick={onRefresh}
            disabled={isFetching}
            aria-label="Refresh requests"
            title="Refresh request history"
          >
            <RefreshCw
              size={14}
              className={isFetching ? "gw-spin" : ""}
              style={{ marginRight: "6px" }}
              aria-hidden="true"
            />
            Refresh
          </Button>

          {hasActiveFilters && (
            <Button
              variant="ghost"
              size="sm"
              onClick={onResetFilters}
              aria-label="Reset all filters"
              leftIcon={<X size={14} aria-hidden="true" />}
            >
              Reset Filters
            </Button>
          )}
        </div>
      </div>

      {/* Custom UTC Range Form */}
      {preset === "custom" && (
        <form
          className="gw-custom-range-form"
          onSubmit={handleApplyCustom}
          data-testid="custom-range-form"
        >
          <div className="gw-custom-range-inputs">
            <div className="gw-custom-input-group">
              <span className="gw-control-label">Start UTC:</span>
              <Input
                type="text"
                placeholder="YYYY-MM-DDTHH:mm:ssZ"
                value={startInput}
                onChange={(e) => setStartInput(e.target.value)}
                aria-label="Start timestamp in UTC"
                style={{ fontFamily: "var(--font-mono)", fontSize: "12px", width: "220px" }}
              />
            </div>
            <ArrowRight size={14} className="gw-custom-range-arrow" aria-hidden="true" />
            <div className="gw-custom-input-group">
              <span className="gw-control-label">End UTC:</span>
              <Input
                type="text"
                placeholder="YYYY-MM-DDTHH:mm:ssZ"
                value={endInput}
                onChange={(e) => setEndInput(e.target.value)}
                aria-label="End timestamp in UTC"
                style={{ fontFamily: "var(--font-mono)", fontSize: "12px", width: "220px" }}
              />
            </div>
            <Button type="submit" size="sm" variant="primary">
              <Calendar size={13} style={{ marginRight: "4px" }} aria-hidden="true" />
              Apply Range
            </Button>
          </div>
          {customError && (
            <div className="gw-custom-range-error" role="alert">
              {customError}
            </div>
          )}
        </form>
      )}
    </div>
  );
};
