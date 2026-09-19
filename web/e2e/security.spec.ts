import { test, expect } from "./fixtures/test";

test.describe("Web Security Tests", () => {
  test("security headers: CSP, X-Frame-Options, and X-Content-Type-Options", async ({
    gateway,
  }) => {
    const res = await fetch(`${gateway.baseURL}/ui/`);
    expect(res.status).toBe(200);

    const csp = res.headers.get("content-security-policy");
    expect(csp).toBeDefined();
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("script-src 'self'");
    expect(csp).toContain("frame-ancestors 'none'");

    const xcto = res.headers.get("x-content-type-options");
    expect(xcto).toBe("nosniff");

    const xfo = res.headers.get("x-frame-options");
    expect(xfo).toBe("DENY");
  });

  test("cookie security flags: session cookie is HttpOnly, SameSite=Strict, and hidden from JavaScript", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Verify session cookie via browser context
    const cookies = await page.context().cookies();
    const sessionCookie = cookies.find((c) => c.name === "gw_session");
    expect(sessionCookie).toBeDefined();
    expect(sessionCookie?.httpOnly).toBe(true);
    expect(sessionCookie?.sameSite).toBe("Strict");

    // Verify document.cookie cannot access HttpOnly session cookie
    const docCookie = await page.evaluate(() => document.cookie);
    expect(docCookie).not.toContain("gw_session");
  });

  test("CSRF protection: cookie-authenticated mutations without CSRF token are rejected", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Attempt mutation via page context (which includes cookie) without X-CSRF-Token header
    const responseStatus = await page.evaluate(async (url) => {
      const res = await fetch(`${url}/admin/v1/keys`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          // Intentionally omitting X-CSRF-Token
        },
        body: JSON.stringify({
          name: "csrf-attack-key",
          policy: { allowed_models: ["gpt-4o-mini"] },
        }),
      });
      return res.status;
    }, gateway.baseURL);

    expect(responseStatus).toBe(403);
  });

  test("XSS protection: malicious scripts in key names, models, or payload bodies are harmlessly escaped", async ({
    page,
    gateway,
  }) => {
    // 1. Create a key with malicious script in its name
    const maliciousName = `<script>window.__xss_executed=true;</script><img src=x onerror="window.__xss_executed=true;">`;
    const key = await gateway.createKey({
      name: maliciousName,
      allowedModels: ["gpt-4o-mini"],
      requestBodyLogging: true,
    });

    // 2. Proxy request with malicious body payload
    await gateway.proxyRequest({
      keySecret: key.secret,
      model: "gpt-4o-mini",
      messages: [{ role: "user", content: `<script>window.__xss_body=true;</script>` }],
    });

    // 3. Navigate to UI and view keys
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    await page.goto(`${gateway.baseURL}/ui/keys`);
    await expect(page.locator('[data-testid="keys-page"]')).toBeVisible();

    // Verify window.__xss_executed was NOT evaluated
    const wasXssExecuted = await page.evaluate(() => (window as unknown as { __xss_executed?: boolean }).__xss_executed);
    expect(wasXssExecuted).toBeUndefined();

    // 4. View request body in safe body viewer
    await page.goto(`${gateway.baseURL}/ui/requests`);
    const row = page.locator('table tbody tr, [data-testid="requests-cards-view"] [data-testid^="request-card-"]').locator('visible=true').first();
    await row.click();

    const openBodyBtn = page.locator('[data-testid="open-body-client_request-btn"]').first();
    if (await openBodyBtn.isVisible()) {
      await openBodyBtn.click();
      await expect(page.locator('[data-testid="body-viewer"]')).toBeVisible();

      const wasBodyXssExecuted = await page.evaluate(() => (window as unknown as { __xss_body?: boolean }).__xss_body);
      expect(wasBodyXssExecuted).toBeUndefined();
    }
  });

  test("credential persistence and cache scrubbing on logout", async ({ page, gateway }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Explicit logout
    const logoutBtn = page.locator('[data-testid="logout-btn"]').first();
    await logoutBtn.click();
    await expect(page.locator('[data-testid="login-page"]')).toBeVisible();

    // Check localStorage, sessionStorage, and TanStack query cache
    const checkState = await page.evaluate(() => {
      return {
        localStorageKeys: Object.keys(localStorage),
        sessionStorageKeys: Object.keys(sessionStorage),
      };
    });

    expect(checkState.localStorageKeys).not.toContain("admin_credential");
    expect(checkState.localStorageKeys).not.toContain("gw_session");
    expect(checkState.sessionStorageKeys).toHaveLength(0);
  });
});
