import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1, // Single worker for deterministic SQLite state and lifecycle
  retries: 0,
  timeout: 30000,
  expect: {
    timeout: 10000,
  },
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  use: {
    trace: "off",
    screenshot: "off",
    video: "off",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        colorScheme: "dark",
      },
      testMatch: /.*\.spec\.ts/,
    },
    {
      name: "chromium-light",
      use: {
        ...devices["Desktop Chrome"],
        colorScheme: "light",
      },
      testMatch: /journeys\.spec\.ts/,
    },
    {
      name: "mobile-chromium",
      use: {
        ...devices["Pixel 5"],
        colorScheme: "dark",
        contextOptions: {
          reducedMotion: "reduce",
        },
      },
      testMatch: /(journeys|performance)\.spec\.ts/,
    },
    {
      name: "firefox",
      use: {
        ...devices["Desktop Firefox"],
        colorScheme: "dark",
      },
      // Smoke subset on Firefox
      testMatch: /journeys\.spec\.ts/,
    },
    {
      name: "webkit",
      use: {
        ...devices["Desktop Safari"],
      },
      testMatch: /journeys\.spec\.ts/,
    },
  ],
});
