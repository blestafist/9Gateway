import { test as base, expect } from "@playwright/test";
import { startGatewayHarness, GatewayInstance } from "./harness";

export const test = base.extend<Record<string, never>, { gateway: GatewayInstance }>({
  gateway: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const gw = await startGatewayHarness();
      await use(gw);
      await gw.cleanup();
    },
    { scope: "worker" },
  ],
});

export { expect };
