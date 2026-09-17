import { QueryClient } from "@tanstack/react-query";
import { bumpGeneration } from "../transport/generation";

export function createAdminQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Conservative default stale time (15 seconds)
        staleTime: 15_000,
        // Garbage collection cache time (5 minutes)
        gcTime: 5 * 60 * 1000,
        // Focus refetch disabled globally to prevent sudden network bursts on focus
        refetchOnWindowFocus: false,
        // Network retry handled at transport level
        retry: false,
      },
      mutations: {
        // Mutations never auto-retry
        retry: false,
      },
    },
  });
}

export const adminQueryClient = createAdminQueryClient();

export function clearAdminCache(queryClient: QueryClient = adminQueryClient): void {
  // Discard any late responses in flight
  bumpGeneration();
  // Clear all cached admin queries and mutations
  queryClient.clear();
}
