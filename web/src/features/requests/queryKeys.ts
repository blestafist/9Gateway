import { RequestBodyKind, RequestListFilters } from "./types";

export const requestQueryKeys = {
  all: ["requests"] as const,
  lists: () => [...requestQueryKeys.all, "list"] as const,
  list: (filters: RequestListFilters = {}) =>
    [...requestQueryKeys.lists(), filters] as const,
  details: () => [...requestQueryKeys.all, "detail"] as const,
  detail: (id: string) => [...requestQueryKeys.details(), id] as const,
  bodies: () => [...requestQueryKeys.all, "body"] as const,
  body: (id: string, kind: RequestBodyKind) =>
    [...requestQueryKeys.bodies(), id, kind] as const,
};
