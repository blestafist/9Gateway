import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { useQuery } from "@tanstack/react-query";
import {
  createAdminQueryClient,
  clearAdminCache,
  AdminQueryProvider,
} from "./index";
import { getGeneration } from "../transport/generation";

describe("TanStack Query Configuration", () => {
  it("initializes QueryClient with conservative defaults", () => {
    const client = createAdminQueryClient();
    const queryDefaults = client.getDefaultOptions().queries;
    const mutationDefaults = client.getDefaultOptions().mutations;

    // Conservative stale time (15 seconds)
    expect(queryDefaults?.staleTime).toBe(15_000);
    // Garbage collection time (5 minutes)
    expect(queryDefaults?.gcTime).toBe(300_000);
    // Focus refetch disabled to prevent request storms
    expect(queryDefaults?.refetchOnWindowFocus).toBe(false);
    // Retry disabled at query level (handled by transport)
    expect(queryDefaults?.retry).toBe(false);
    // Mutations never auto-retry
    expect(mutationDefaults?.retry).toBe(false);
  });

  it("clears query cache and advances generation barrier on clearAdminCache", () => {
    const client = createAdminQueryClient();
    client.setQueryData(["test-key"], { data: "sample" });
    expect(client.getQueryData(["test-key"])).toEqual({ data: "sample" });

    const genBefore = getGeneration();
    clearAdminCache(client);
    const genAfter = getGeneration();

    expect(genAfter).toBeGreaterThan(genBefore);
    expect(client.getQueryData(["test-key"])).toBeUndefined();
  });

  it("AdminQueryProvider supplies QueryClient to React tree", async () => {
    const client = createAdminQueryClient();

    const TestComponent = () => {
      const { data } = useQuery({
        queryKey: ["greeting"],
        queryFn: () => "ready",
      });
      return <div>Status: {data}</div>;
    };

    render(
      <AdminQueryProvider client={client}>
        <TestComponent />
      </AdminQueryProvider>
    );

    expect(await screen.findByText("Status: ready")).toBeInTheDocument();
  });
});
