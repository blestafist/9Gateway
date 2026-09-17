import { describe, it, expect } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useCursorPagination, sanitizeShareableParams } from "./index";

describe("Pagination", () => {
  describe("useCursorPagination", () => {
    it("initializes with default page 1 and no cursor", () => {
      const { result } = renderHook(() => useCursorPagination());

      expect(result.current.page).toBe(1);
      expect(result.current.currentCursor).toBeUndefined();
      expect(result.current.hasNextPage).toBe(false);
      expect(result.current.hasPrevPage).toBe(false);
    });

    it("advances to next page and updates cursor", () => {
      const { result, rerender } = renderHook(
        ({ nextCursor }) => useCursorPagination({ nextCursor }),
        { initialProps: { nextCursor: "cursor_page_2" } }
      );

      expect(result.current.hasNextPage).toBe(true);
      expect(result.current.hasPrevPage).toBe(false);

      act(() => {
        result.current.goToNextPage();
      });

      expect(result.current.page).toBe(2);
      expect(result.current.currentCursor).toBe("cursor_page_2");
      expect(result.current.hasPrevPage).toBe(true);

      // Supply next cursor for page 3
      rerender({ nextCursor: "cursor_page_3" });
      act(() => {
        result.current.goToNextPage();
      });

      expect(result.current.page).toBe(3);
      expect(result.current.currentCursor).toBe("cursor_page_3");

      // Go back to page 2
      act(() => {
        result.current.goToPrevPage();
      });

      expect(result.current.page).toBe(2);
      expect(result.current.currentCursor).toBe("cursor_page_2");

      // Go back to page 1
      act(() => {
        result.current.goToPrevPage();
      });

      expect(result.current.page).toBe(1);
      expect(result.current.currentCursor).toBeUndefined();
      expect(result.current.hasPrevPage).toBe(false);
    });

    it("resets pagination when filterKey changes", () => {
      const { result, rerender } = renderHook(
        ({ filterKey, nextCursor }: { filterKey?: string; nextCursor?: string | null }) =>
          useCursorPagination({ filterKey, nextCursor }),
        { initialProps: { filterKey: "status=200", nextCursor: "cursor_1" as string | null | undefined } }
      );

      act(() => {
        result.current.goToNextPage();
      });

      expect(result.current.page).toBe(2);
      expect(result.current.currentCursor).toBe("cursor_1");

      // Filter changes (e.g. user selected different status or model)
      rerender({ filterKey: "status=500", nextCursor: undefined });

      expect(result.current.page).toBe(1);
      expect(result.current.currentCursor).toBeUndefined();
      expect(result.current.hasPrevPage).toBe(false);
    });

    it("resets explicitly when resetPagination is called", () => {
      const { result } = renderHook(() =>
        useCursorPagination({ nextCursor: "cursor_abc" })
      );

      act(() => {
        result.current.goToNextPage();
      });
      expect(result.current.page).toBe(2);

      act(() => {
        result.current.resetPagination();
      });

      expect(result.current.page).toBe(1);
      expect(result.current.currentCursor).toBeUndefined();
      expect(result.current.hasPrevPage).toBe(false);
    });
  });

  describe("sanitizeShareableParams", () => {
    it("strips cursors and secrets while preserving valid search filters", () => {
      const params = new URLSearchParams({
        model: "gpt-4o",
        status: "200",
        cursor: "opaque_secret_cursor_xyz",
        next_cursor: "opaque_secret_cursor_abc",
        token: "session_token_123",
        key: "raw_key_secret",
        secret: "supersecret",
      });

      const sanitized = sanitizeShareableParams(params);

      expect(sanitized.get("model")).toBe("gpt-4o");
      expect(sanitized.get("status")).toBe("200");
      expect(sanitized.has("cursor")).toBe(false);
      expect(sanitized.has("next_cursor")).toBe(false);
      expect(sanitized.has("token")).toBe(false);
      expect(sanitized.has("key")).toBe(false);
      expect(sanitized.has("secret")).toBe(false);
    });
  });
});
