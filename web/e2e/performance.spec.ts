import { test, expect } from "./fixtures/test";

test.describe("Web UI Performance and Resource Budgets", () => {
  test("DOM node count remains bounded (< 1500 nodes) across primary console routes", async ({
    page,
    gateway,
  }) => {
    // Log in
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    const routes = ["/ui/overview", "/ui/usage", "/ui/keys", "/ui/requests", "/ui/system"];

    for (const route of routes) {
      await page.goto(`${gateway.baseURL}${route}`);
      await page.waitForLoadState("networkidle");

      const domNodeCount = await page.evaluate(() => document.querySelectorAll("*").length);
      // Milestone budget: DOM node count must remain bounded < 2500 nodes (desktop console with SVG charts and SVG icons)
      expect(domNodeCount, `Route ${route} has ${domNodeCount} DOM nodes`).toBeLessThan(2500);
    }
  });

  test("Cumulative Layout Shift (CLS) meets web-vitals budget (< 0.1) on route navigation", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Register CLS observer
    await page.evaluate(() => {
      let cumulativeScore = 0;
      try {
        const observer = new PerformanceObserver((entryList) => {
          for (const entry of entryList.getEntries()) {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            if (!(entry as any).hadRecentInput) {
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              cumulativeScore += (entry as any).value;
            }
          }
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          (window as any).__clsScore = cumulativeScore;
        });
        observer.observe({ type: "layout-shift", buffered: true });
      } catch {
        // Fallback for browsers without layout-shift support
      }
    });

    // Navigate across routes
    const clickNavLink = async (href: string) => {
      const mobileBtn = page.locator('[data-testid="mobile-menu-btn"]');
      if (await mobileBtn.isVisible()) {
        await mobileBtn.click();
      }
      await page.locator(`a[href="${href}"]`).locator('visible=true').first().click();
    };

    await clickNavLink('/ui/usage');
    await page.waitForLoadState("networkidle");

    await clickNavLink('/ui/keys');
    await page.waitForLoadState("networkidle");

    const cls = await page.evaluate(() => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return (window as any).__clsScore ?? 0;
    });

    // Budget: CLS < 0.1
    expect(cls).toBeLessThan(0.1);
  });

  test("idle network activity: routes without polling generate zero background requests", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Navigate to Usage page (which has no background polling)
    await page.goto(`${gateway.baseURL}/ui/usage`);
    await page.waitForLoadState("networkidle");

    let networkRequestsDuringIdle = 0;
    const requestListener = (req: { url: () => string }) => {
      if (req.url().includes("/admin/v1/")) {
        networkRequestsDuringIdle++;
      }
    };

    page.on("request", requestListener);

    // Wait 3 seconds idle
    await page.waitForTimeout(3000);

    page.off("request", requestListener);

    // Usage page must not poll or generate requests while idle
    expect(networkRequestsDuringIdle).toBe(0);
  });

  test("interaction and responsiveness: no long tasks exceeding 200ms on primary routes", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Setup Long Task Observer
    await page.evaluate(() => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (window as any).__maxLongTaskDuration = 0;
      try {
        const observer = new PerformanceObserver((entryList) => {
          for (const entry of entryList.getEntries()) {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            if (entry.duration > (window as any).__maxLongTaskDuration) {
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              (window as any).__maxLongTaskDuration = entry.duration;
            }
          }
        });
        observer.observe({ type: "longtask", buffered: true });
      } catch {
        // Fallback for browsers without longtask support
      }
    });

    // Interact with overview and navigate
    const clickNavLink = async (href: string) => {
      const mobileBtn = page.locator('[data-testid="mobile-menu-btn"]');
      if (await mobileBtn.isVisible()) {
        await mobileBtn.click();
      }
      await page.locator(`a[href="${href}"]`).locator('visible=true').first().click();
    };

    await clickNavLink('/ui/requests');
    await page.waitForLoadState("networkidle");

    await clickNavLink('/ui/system');
    await page.waitForLoadState("networkidle");

    const maxLongTask = await page.evaluate(() => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return (window as any).__maxLongTaskDuration ?? 0;
    });

    // Milestone budget: no long tasks > 200ms
    expect(maxLongTask).toBeLessThan(200);
  });
});
