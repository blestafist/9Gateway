import { useState, useCallback, useEffect, useRef } from "react";

export interface CursorPaginationState {
  currentCursor: string | undefined;
  cursorHistory: string[];
  page: number;
}

export interface UseCursorPaginationOptions {
  /**
   * Changing this key (e.g. JSON string of filter parameters) resets pagination
   * back to the first page.
   */
  filterKey?: string;
  nextCursor?: string | null;
}

export interface CursorPaginationResult {
  currentCursor: string | undefined;
  page: number;
  hasNextPage: boolean;
  hasPrevPage: boolean;
  goToNextPage: (explicitNextCursor?: string) => void;
  goToPrevPage: () => void;
  resetPagination: () => void;
}

export function useCursorPagination(
  options: UseCursorPaginationOptions = {}
): CursorPaginationResult {
  const { filterKey, nextCursor } = options;

  const [currentCursor, setCurrentCursor] = useState<string | undefined>(undefined);
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [page, setPage] = useState<number>(1);

  const prevFilterKeyRef = useRef(filterKey);

  const resetPagination = useCallback(() => {
    setCurrentCursor(undefined);
    setCursorHistory([]);
    setPage(1);
  }, []);

  // When filterKey changes, reset cursor pagination to page 1
  useEffect(() => {
    if (prevFilterKeyRef.current !== filterKey) {
      prevFilterKeyRef.current = filterKey;
      resetPagination();
    }
  }, [filterKey, resetPagination]);

  const goToNextPage = useCallback(
    (explicitNextCursor?: string) => {
      const target = explicitNextCursor ?? nextCursor;
      if (!target) {
        return;
      }
      setCursorHistory((prev) => [...prev, currentCursor ?? ""]);
      setCurrentCursor(target);
      setPage((prev) => prev + 1);
    },
    [currentCursor, nextCursor]
  );

  const goToPrevPage = useCallback(() => {
    setCursorHistory((prev) => {
      if (prev.length === 0) {
        return prev;
      }
      const updated = [...prev];
      const previousCursor = updated.pop();
      setCurrentCursor(previousCursor === "" ? undefined : previousCursor);
      setPage((p) => Math.max(1, p - 1));
      return updated;
    });
  }, []);

  return {
    currentCursor,
    page,
    hasNextPage: Boolean(nextCursor),
    hasPrevPage: cursorHistory.length > 0,
    goToNextPage,
    goToPrevPage,
    resetPagination,
  };
}

/**
 * Returns a copy of URLSearchParams with cursor and sensitive parameters removed,
 * ensuring opaque cursors and credentials are never leaked into browser history or bookmarks.
 */
export function sanitizeShareableParams(params: URLSearchParams): URLSearchParams {
  const sanitized = new URLSearchParams(params);
  sanitized.delete("cursor");
  sanitized.delete("next_cursor");
  sanitized.delete("token");
  sanitized.delete("key");
  sanitized.delete("secret");
  return sanitized;
}
