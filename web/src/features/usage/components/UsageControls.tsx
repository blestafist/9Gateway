import React, { useState } from "react";
import {
  Tabs,
  Select,
  Input,
  Button,
} from "../../../shared/ui";
import {
  UsageRangePreset,
  UsageBucketResolution,
} from "../types";
import { getValidBuckets, formatBucketLabel } from "../ranges";
import { RefreshCw, Calendar, ArrowRight } from "lucide-react";

export interface UsageControlsProps {
  preset: UsageRangePreset;
  onPresetChange: (preset: UsageRangePreset) => void;
  bucket: UsageBucketResolution;
  onBucketChange: (bucket: UsageBucketResolution) => void;
  customFrom?: string;
  customTo?: string;
  onCustomRangeApply: (from: string, to: string) => void;
  isFetching: boolean;
  onRefresh: () => void;
}

const PRESET_TABS = [
  { id: "1h", label: "1h" },
  { id: "today", label: "Today" },
  { id: "24h", label: "24h" },
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
  { id: "90d", label: "90d" },
  { id: "1y", label: "1y" },
  { id: "all", label: "All retained" },
  { id: "custom", label: "Custom UTC" },
];

export const UsageControls: React.FC<UsageControlsProps> = ({
  preset,
  onPresetChange,
  bucket,
  onBucketChange,
  customFrom = "",
  customTo = "",
  onCustomRangeApply,
  isFetching,
  onRefresh,
}) => {
  const [startInput, setStartInput] = useState(customFrom);
  const [endInput, setEndInput] = useState(customTo);
  const [customError, setCustomError] = useState<string | null>(null);

  // Compute valid buckets for current preset/custom range
  let customDurationMs: number | undefined;
  if (preset === "custom" && customFrom && customTo) {
    const s = new Date(customFrom).getTime();
    const e = new Date(customTo).getTime();
    if (!Number.isNaN(s) && !Number.isNaN(e) && e > s) {
      customDurationMs = e - s;
    }
  }

  const validBuckets = getValidBuckets(preset, customDurationMs);

  const bucketOptions = validBuckets.map((b) => ({
    value: b,
    label: formatBucketLabel(b),
  }));

  const handleApplyCustom = (e: React.FormEvent) => {
    e.preventDefault();
    setCustomError(null);

    if (!startInput.trim() || !endInput.trim()) {
      setCustomError("Both start and end UTC timestamps are required.");
      return;
    }

    const startDate = new Date(startInput.trim());
    const endDate = new Date(endInput.trim());

    if (Number.isNaN(startDate.getTime())) {
      setCustomError("Invalid start timestamp. Expected RFC3339/ISO format (e.g. 2026-09-01T00:00:00Z).");
      return;
    }
    if (Number.isNaN(endDate.getTime())) {
      setCustomError("Invalid end timestamp. Expected RFC3339/ISO format (e.g. 2026-09-02T00:00:00Z).");
      return;
    }
    if (startDate.getTime() >= endDate.getTime()) {
      setCustomError("Start timestamp must be strictly before end timestamp.");
      return;
    }

    onCustomRangeApply(startDate.toISOString(), endDate.toISOString());
  };

  return (
    <div className="gw-usage-controls-container">
      <div className="gw-usage-controls-bar">
        {/* Presets Bar */}
        <div className="gw-usage-presets">
          <span className="gw-control-label">Range:</span>
          <Tabs
            items={PRESET_TABS}
            activeTab={preset}
            onChange={(tabId) => onPresetChange(tabId as UsageRangePreset)}
            aria-label="Time range"
          />
        </div>

        {/* Bucket selector & Refresh button */}
        <div className="gw-usage-actions">
          <div className="gw-usage-bucket-select">
            <span className="gw-control-label" style={{ marginRight: "6px" }}>
              Bucket:
            </span>
            <Select
              aria-label="Bucket resolution"
              value={bucket}
              onChange={(e) => onBucketChange(e.target.value as UsageBucketResolution)}
              options={bucketOptions}
              style={{ minWidth: "110px", padding: "4px 8px", height: "32px" }}
            />
          </div>

          <Button
            variant="outline"
            size="sm"
            onClick={onRefresh}
            disabled={isFetching}
            aria-label="Refresh usage data"
            title="Refresh usage analytics"
          >
            <RefreshCw
              size={14}
              className={isFetching ? "gw-spin" : ""}
              style={{ marginRight: "6px" }}
              aria-hidden="true"
            />
            Refresh
          </Button>
        </div>
      </div>

      {/* Custom UTC Range Input Drawer / Form */}
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
                style={{ fontFamily: "var(--font-mono)", fontSize: "12px", width: "230px" }}
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
                style={{ fontFamily: "var(--font-mono)", fontSize: "12px", width: "230px" }}
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
