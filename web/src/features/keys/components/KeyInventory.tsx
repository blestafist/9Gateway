import React from "react";
import { AdminKeyListItem, KeyPageFilters } from "../types";
import { KeyFilterBar } from "./KeyFilterBar";
import { KeyTable } from "./KeyTable";
import { KeyCardList } from "./KeyCardList";
import { KeyPagination } from "./KeyPagination";
import { EmptyState, Skeleton, Button } from "../../../shared/ui";

export interface KeyInventoryProps {
  keys: AdminKeyListItem[];
  filteredKeys: AdminKeyListItem[];
  isLoading: boolean;
  isFetching: boolean;
  filters: KeyPageFilters;
  onFilterChange: (updated: Partial<KeyPageFilters>) => void;
  onResetFilters: () => void;
  pageSize: number;
  onPageSizeChange: (newPageSize: number) => void;
  page: number;
  hasNextPage: boolean;
  hasPrevPage: boolean;
  onNextPage: () => void;
  onPrevPage: () => void;
  onSelectKey: (keyId: string) => void;
}

export const KeyInventory: React.FC<KeyInventoryProps> = ({
  keys,
  filteredKeys,
  isLoading,
  isFetching,
  filters,
  onFilterChange,
  onResetFilters,
  pageSize,
  onPageSizeChange,
  page,
  hasNextPage,
  hasPrevPage,
  onNextPage,
  onPrevPage,
  onSelectKey,
}) => {
  return (
    <div className="gw-key-inventory-root" data-testid="key-inventory">
      {/* Search and Filters */}
      <KeyFilterBar
        filters={filters}
        onFilterChange={onFilterChange}
        onResetFilters={onResetFilters}
        pageSize={pageSize}
        onPageSizeChange={onPageSizeChange}
        filteredCount={filteredKeys.length}
        totalCount={keys.length}
      />

      {/* Loading Skeleton */}
      {isLoading && keys.length === 0 && (
        <div
          data-testid="keys-loading-skeleton"
          style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}
        >
          <Skeleton height="44px" />
          <Skeleton height="44px" />
          <Skeleton height="44px" />
        </div>
      )}

      {/* Empty State: No keys on page */}
      {!isLoading && keys.length === 0 && (
        <EmptyState
          title="No API Keys Found"
          description="No gateway API keys have been created yet. Create a key using the admin CLI or API to begin routing traffic."
          data-testid="keys-empty-state"
        />
      )}

      {/* Empty Filter State: Keys exist, but filter matches none */}
      {!isLoading && keys.length > 0 && filteredKeys.length === 0 && (
        <EmptyState
          title="No Matching Keys on This Page"
          description="No keys on the current page match your active search and filter criteria. Note that filtering operates on the loaded page only."
          action={
            <Button variant="secondary" size="sm" onClick={onResetFilters}>
              Clear Filters
            </Button>
          }
          data-testid="keys-empty-filter-state"
        />
      )}

      {/* Desktop Table View */}
      {filteredKeys.length > 0 && (
        <KeyTable keys={filteredKeys} onSelectKey={onSelectKey} />
      )}

      {/* Mobile Deliberate Cards View */}
      {filteredKeys.length > 0 && (
        <KeyCardList keys={filteredKeys} onSelectKey={onSelectKey} />
      )}

      {/* Cursor Pagination Bar */}
      {!isLoading && keys.length > 0 && (
        <KeyPagination
          page={page}
          hasNextPage={hasNextPage}
          hasPrevPage={hasPrevPage}
          onNextPage={onNextPage}
          onPrevPage={onPrevPage}
          isFetching={isFetching}
          itemCount={filteredKeys.length}
        />
      )}
    </div>
  );
};
