import React from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "../../../shared/ui";

export interface RequestPaginationProps {
  page: number;
  hasNextPage: boolean;
  hasPrevPage: boolean;
  onNextPage: () => void;
  onPrevPage: () => void;
  isFetching: boolean;
  itemCount: number;
}

export const RequestPagination: React.FC<RequestPaginationProps> = ({
  page,
  hasNextPage,
  hasPrevPage,
  onNextPage,
  onPrevPage,
  isFetching,
  itemCount,
}) => {
  return (
    <nav
      className="gw-requests-pagination"
      aria-label="Requests pagination"
      data-testid="requests-pagination"
    >
      <div className="gw-requests-pagination-info">
        <span>
          Page {page} • {itemCount} {itemCount === 1 ? "request" : "requests"} on page
        </span>
      </div>

      <div className="gw-requests-pagination-actions">
        <Button
          variant="secondary"
          size="sm"
          leftIcon={<ChevronLeft size={16} aria-hidden="true" />}
          onClick={onPrevPage}
          disabled={!hasPrevPage || isFetching}
          aria-label="Go to previous page"
        >
          Previous
        </Button>

        <span className="gw-requests-page-indicator" aria-current="page">
          Page {page}
        </span>

        <Button
          variant="secondary"
          size="sm"
          rightIcon={<ChevronRight size={16} aria-hidden="true" />}
          onClick={onNextPage}
          disabled={!hasNextPage || isFetching}
          aria-label="Go to next page"
        >
          Next
        </Button>
      </div>

      {/* Screen reader announcement for page changes */}
      <div className="gw-sr-only" aria-live="polite" aria-atomic="true">
        Page {page}, displaying {itemCount} requests.
      </div>
    </nav>
  );
};
