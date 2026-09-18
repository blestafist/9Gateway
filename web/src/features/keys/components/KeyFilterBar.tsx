import React from "react";
import { Search, X } from "lucide-react";
import { Input, Select, Button, Badge } from "../../../shared/ui";
import { KeyPageFilters, KeyPolicyFilter, KeyStatusFilter } from "../types";

export interface KeyFilterBarProps {
  filters: KeyPageFilters;
  onFilterChange: (filters: Partial<KeyPageFilters>) => void;
  onResetFilters: () => void;
  pageSize: number;
  onPageSizeChange: (newSize: number) => void;
  filteredCount: number;
  totalCount: number;
}

const STATUS_OPTIONS: { value: KeyStatusFilter; label: string }[] = [
  { value: "all", label: "All Statuses" },
  { value: "active", label: "Active / Enabled" },
  { value: "disabled", label: "Disabled" },
  { value: "expired", label: "Expired" },
  { value: "expiring", label: "Expiring Soon (<= 30d)" },
];

const POLICY_OPTIONS: { value: KeyPolicyFilter; label: string }[] = [
  { value: "all", label: "All Policies" },
  { value: "allowlist", label: "Has Model Allowlist" },
  { value: "denylist", label: "Has Model Denylist" },
  { value: "log_req", label: "Logs Request Body" },
  { value: "log_res", label: "Logs Response Body" },
];

const PAGE_SIZE_OPTIONS = [
  { value: "10", label: "10 per page" },
  { value: "25", label: "25 per page" },
  { value: "50", label: "50 per page" },
  { value: "100", label: "100 per page" },
];

export const KeyFilterBar: React.FC<KeyFilterBarProps> = ({
  filters,
  onFilterChange,
  onResetFilters,
  pageSize,
  onPageSizeChange,
  filteredCount,
  totalCount,
}) => {
  const hasActiveFilters =
    Boolean(filters.searchQuery.trim()) ||
    filters.status !== "all" ||
    filters.policy !== "all";

  return (
    <div className="gw-keys-filter-bar" data-testid="keys-filter-bar">
      <div className="gw-keys-filter-inputs">
        {/* Client-side search input */}
        <div className="gw-keys-search-field">
          <Input
            value={filters.searchQuery}
            onChange={(e) => onFilterChange({ searchQuery: e.target.value })}
            placeholder="Filter current page (name, ID, prefix)..."
            leftIcon={<Search size={16} aria-hidden="true" />}
            aria-label="Filter current page by name, ID, or prefix"
          />
        </div>

        {/* Status filter select */}
        <div className="gw-keys-filter-select">
          <Select
            value={filters.status}
            onChange={(e) => onFilterChange({ status: e.target.value as KeyStatusFilter })}
            options={STATUS_OPTIONS}
            aria-label="Filter by status"
          />
        </div>

        {/* Policy filter select */}
        <div className="gw-keys-filter-select">
          <Select
            value={filters.policy}
            onChange={(e) => onFilterChange({ policy: e.target.value as KeyPolicyFilter })}
            options={POLICY_OPTIONS}
            aria-label="Filter by policy"
          />
        </div>

        {/* Page size select */}
        <div className="gw-keys-pagesize-select">
          <Select
            value={String(pageSize)}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
            options={PAGE_SIZE_OPTIONS}
            aria-label="Items per page"
          />
        </div>
      </div>

      {/* Scope disclaimer notice bar */}
      <div className="gw-keys-filter-notice" role="status" aria-live="polite">
        <div className="gw-keys-filter-notice-text">
          <Badge variant="neutral" size="sm" className="gw-keys-filter-scope-badge">
            Current-page filter only
          </Badge>
          <span>
            Showing {filteredCount} of {totalCount} keys on this page. Server-wide search is not supported.
          </span>
        </div>

        {hasActiveFilters && (
          <Button
            variant="ghost"
            size="sm"
            leftIcon={<X size={14} aria-hidden="true" />}
            onClick={onResetFilters}
            aria-label="Reset current page filters"
          >
            Clear filters
          </Button>
        )}
      </div>
    </div>
  );
};
