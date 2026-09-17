import { KeyListFilters } from "./types";

export const keyQueryKeys = {
  all: ["keys"] as const,
  lists: () => [...keyQueryKeys.all, "list"] as const,
  list: (filters: KeyListFilters = {}) =>
    [...keyQueryKeys.lists(), filters] as const,
  details: () => [...keyQueryKeys.all, "detail"] as const,
  detail: (id: string) => [...keyQueryKeys.details(), id] as const,
};
