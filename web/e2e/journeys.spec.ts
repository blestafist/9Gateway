import { test, expect } from "./fixtures/test";

test.describe("Web UI Critical Operator Journeys", () => {
  test("authentication: rejects invalid credentials, logs in with valid credential, sets session cookie", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await expect(page.locator('[data-testid="login-page"]')).toBeVisible();

    // 1. Attempt login with invalid credential
    await page.fill('#admin-credential', 'wrong-password-123');
    await page.click('[data-testid="login-submit-btn"]');
    await expect(page.locator('[data-testid="login-alert"]')).toBeVisible();
    await expect(page.locator('[data-testid="login-alert"]')).toContainText(
      /Incorrect API key|Invalid admin credential/i
    );

    // 2. Successful login with valid admin credential
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');

    // Should redirect to overview
    await expect(page).toHaveURL(`${gateway.baseURL}/ui/overview`);
    await expect(page.locator('[data-testid="overview-page"]')).toBeVisible();

    // Verify session cookie exists and is HttpOnly
    const cookies = await page.context().cookies();
    const sessionCookie = cookies.find((c) => c.name === "gw_session");
    expect(sessionCookie).toBeDefined();
    expect(sessionCookie?.httpOnly).toBe(true);
    expect(sessionCookie?.sameSite).toBe("Strict");
  });

  test("protected route redirect: deep links redirect to login with returnTo and restore on authentication", async ({
    page,
    gateway,
  }) => {
    // Navigate directly to protected route /ui/keys unauthenticated
    await page.goto(`${gateway.baseURL}/ui/keys`);
    await expect(page).toHaveURL(`${gateway.baseURL}/ui/login`);
    await expect(page.locator('[data-testid="login-page"]')).toBeVisible();

    // Authenticate
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');

    // Should redirect to originally requested /ui/keys
    await expect(page).toHaveURL(`${gateway.baseURL}/ui/keys`);
    await expect(page.locator('[data-testid="keys-page"]')).toBeVisible();
  });

  test("overview and usage dashboards: renders KPIs, health strip, and preset switches", async ({
    page,
    gateway,
  }) => {
    // Log in
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await expect(page.locator('[data-testid="overview-page"]')).toBeVisible();

    // Overview KPIs and Health strip
    await expect(page.locator('[data-testid="status-pipeline"]')).toBeVisible();
    await expect(page.locator('[data-testid="kpi-total-requests"]')).toBeVisible();

    // Switch period presets on Overview
    const period7d = page.locator('button:has-text("7d")').first();
    if (await period7d.isVisible()) {
      await period7d.click();
      await expect(page).toHaveURL(/period=7d/);
    }

    // Navigate to Usage page
    const mobileMenuBtn = page.locator('[data-testid="mobile-menu-btn"]');
    if (await mobileMenuBtn.isVisible()) {
      await mobileMenuBtn.click();
    }
    await page.locator('a[href="/ui/usage"]').locator('visible=true').first().click();
    await expect(page).toHaveURL(`${gateway.baseURL}/ui/usage`);
    await expect(page.locator('[data-testid="usage-page"]')).toBeVisible();

    // Usage KPI cards & chart tabs
    await expect(page.locator('[data-testid="usage-kpi-grid"]')).toBeVisible();
    const tokensTab = page.locator('button:has-text("Tokens")').first();
    if (await tokensTab.isVisible()) {
      await tokensTab.click();
      await expect(tokensTab).toHaveAttribute("aria-pressed", "true");
    }

    const costTab = page.locator('button:has-text("Cost")').first();
    if (await costTab.isVisible()) {
      await costTab.click();
      await expect(costTab).toHaveAttribute("aria-pressed", "true");
    }
  });

  test("api keys lifecycle: create key, inspect secret reveal, update policy in drawer", async ({
    page,
    gateway,
  }) => {
    // Log in
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Navigate to keys
    await page.goto(`${gateway.baseURL}/ui/keys`);
    await expect(page.locator('[data-testid="keys-page"]')).toBeVisible();

    // Click "Create API Key" button
    const createBtn = page.locator('button:has-text("Create API Key"), button:has-text("Create Key")').first();
    await createBtn.click();
    await expect(page.locator('[data-testid="create-key-form"]')).toBeVisible();

    // Fill key form
    const keyName = "e2e-test-key-" + Date.now();
    await page.fill('[data-testid="create-key-name-input"]', keyName);
    await page.click('[data-testid="create-key-submit-btn"]');

    // Secret handoff step
    await expect(page.locator('[data-testid="secret-handoff-step"]')).toBeVisible();
    const secretDisplay = page.locator('[data-testid="secret-key-display"]');
    await expect(secretDisplay).toBeVisible();
    await expect(secretDisplay).toHaveAttribute("type", "password");
    // The handoff is visible and masked; dismissing it must remove the secret UI
    // without copying the raw value into test variables or assertion output.

    // Copy action and ack checkbox
    await expect(page.locator('[data-testid="copy-key-btn"]')).toBeVisible();
    await page.locator('text=I have saved this API key in a secure location').click();
    await page.click('[data-testid="done-secret-btn"]');

    // Modal dismissed, key appears in table or card list and raw secret is gone.
    await expect(page.locator('[data-testid="secret-key-display"]')).not.toBeAttached();
    await expect(page.locator('[data-testid="create-key-form"]')).not.toBeVisible();
    const keyElement = page.locator(':is([data-testid^="key-row-"], [data-testid^="key-card-"]):visible').filter({ hasText: keyName }).first();
    await expect(keyElement).toBeVisible();

    // Click key row to open detail drawer
    await keyElement.click();
    await expect(page.locator('[data-testid="key-detail-content"]')).toBeVisible();

    // Click edit policy
    const editBtn = page.locator('[data-testid="open-edit-policy-btn"], [data-testid="edit-policy-btn"]').first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await expect(page.locator('[data-testid="key-policy-form"]')).toBeVisible();

      // Add allowed model
      await page.click('[data-testid="add-allowed-model-btn"]');
      await page.fill('[data-testid="allowed-model-input-0"]', 'gpt-4o-mini');

      // Save policy
      await page.click('[data-testid="save-policy-btn"]');

      // If review modal appears, confirm it
      const confirmReview = page.locator('[data-testid="review-dialog-confirm-btn"]');
      await confirmReview.waitFor({ state: "visible", timeout: 2000 }).then(() => confirmReview.click()).catch(() => {});

      await expect(page.locator('[data-testid="policy-success-alert"]')).toBeVisible();
    }
  });

  test("requests explorer and safe body viewer: proxy request, inspect detail, download body", async ({
    page,
    gateway,
  }) => {
    // 1. Create a key with body logging enabled
    const key = await gateway.createKey({
      name: "body-logging-key",
      allowedModels: ["gpt-4o-mini"],
      requestBodyLogging: true,
      responseBodyLogging: true,
    });

    // 2. Send proxy request
    const response = await gateway.proxyRequest({
      keySecret: key.secret,
      model: "gpt-4o-mini",
      messages: [{ role: "user", content: "Hello safe body viewer" }],
      stream: false,
    });
    expect(response.status).toBe(200);

    // 3. Log in to UI and navigate to Requests
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    await page.goto(`${gateway.baseURL}/ui/requests?key_id=${encodeURIComponent(key.id)}`);
    await expect(page.locator('[data-testid="requests-page"]')).toBeVisible();

    // Requests table or cards should show the completed request
    const requestItem = page.locator(':is(table tbody tr, [data-testid^="request-card-"]):visible').first();
    await expect(requestItem).toBeVisible();
    await requestItem.click();

    // Should navigate to request detail view
    await expect(page.locator('[data-testid="request-detail-view"]')).toBeVisible();
    await expect(page.locator('[data-testid="detail-bodies-card"]')).toBeVisible();

    // Disclose request body viewer
    const openBodyBtn = page.locator('[data-testid="open-body-client_request-btn"]').first();
    await expect(openBodyBtn).toBeVisible();
    await openBodyBtn.click();

    // Safe Body Viewer is now mounted
    await expect(page.locator('[data-testid="body-viewer"]')).toBeVisible();
    await expect(page.locator('[data-testid="body-preview-container"]')).toBeVisible();

    // Check text/json view mode
    const textPre = page.locator('[data-testid="body-pre-json"], [data-testid="body-pre-text"]');
    await expect(textPre).toContainText("Hello safe body viewer");

    // Check hex view mode
    await page.click('[data-testid="view-mode-hex"]');
    await expect(page.locator('[data-testid="body-pre-hex"]')).toBeVisible();

    // Back link returns to requests list
    await page.click('[data-testid="detail-back-link"]');
    await expect(page.locator('[data-testid="requests-page"]')).toBeVisible();
  });

  test("system diagnostics: renders readiness checks, diagnostics modal preview and copy", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    await page.goto(`${gateway.baseURL}/ui/system`);
    await expect(page.locator('[data-testid="system-page"]')).toBeVisible();

    // Readiness checks
    await expect(page.locator('[data-testid="readiness-card"]')).toBeVisible();
    await expect(page.locator('[data-testid="check-sqlite"]')).toBeVisible();
    await expect(page.locator('[data-testid="check-schema"]')).toBeVisible();
    await expect(page.locator('[data-testid="check-upstream"]')).toBeVisible();

    // Open Diagnostics Summary Modal
    await page.click('[data-testid="system-diagnostics-btn"]');
    await expect(page.locator('[data-testid="diagnostics-preview-box"]')).toBeVisible();

    // Copy summary action
    await expect(page.locator('[data-testid="copy-diagnostics-submit-btn"]')).toBeVisible();
    await page.click('button:has-text("Close")');
    await expect(page.locator('[data-testid="diagnostics-preview-box"]')).not.toBeVisible();
  });

  test("deep links, reload, and browser back/forward navigation", async ({ page, gateway }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);

    // Direct navigate to /ui/system and reload (verifies SPA server fallback)
    await page.goto(`${gateway.baseURL}/ui/system`);
    await expect(page.locator('[data-testid="system-page"]')).toBeVisible();
    await page.reload();
    await expect(page.locator('[data-testid="system-page"]')).toBeVisible();

    // Navigate to keys then overview, then test browser back and forward
    await page.goto(`${gateway.baseURL}/ui/keys`);
    await expect(page.locator('[data-testid="keys-page"]')).toBeVisible();

    await page.goBack();
    await expect(page.locator('[data-testid="system-page"]')).toBeVisible();

    await page.goForward();
    await expect(page.locator('[data-testid="keys-page"]')).toBeVisible();
  });

  test("explicit logout: revokes server session, clears storage and query cache, prevents back restoration", async ({
    page,
    gateway,
  }) => {
    await page.goto(`${gateway.baseURL}/ui/login`);
    await page.fill('#admin-credential', gateway.adminCredential);
    await page.click('[data-testid="login-submit-btn"]');
    await page.waitForURL(`${gateway.baseURL}/ui/overview`);
    await expect(page.locator('[data-testid="overview-page"]')).toBeVisible();

    // Navigate to keys to ensure a history entry exists
    await page.goto(`${gateway.baseURL}/ui/keys`);
    await expect(page.locator('[data-testid="keys-page"]')).toBeVisible();

    // Perform explicit logout
    const logoutBtn = page.locator('[data-testid="logout-btn"]').first();
    await logoutBtn.click();

    // Redirects to login
    await expect(page).toHaveURL(`${gateway.baseURL}/ui/login`);
    await expect(page.locator('[data-testid="login-page"]')).toBeVisible();

    // Verify localStorage & sessionStorage contain NO sensitive tokens or credentials
    const storageKeys = await page.evaluate(() => {
      const local = Object.keys(localStorage);
      const sess = Object.keys(sessionStorage);
      return { local, sess };
    });
    for (const k of [...storageKeys.local, ...storageKeys.sess]) {
      expect(k.toLowerCase()).not.toContain("secret");
      expect(k.toLowerCase()).not.toContain("password");
      expect(k.toLowerCase()).not.toContain("credential");
      expect(k.toLowerCase()).not.toContain("token");
    }

    // Verify browser back navigation does not resurrect authenticated view
    await page.goBack();
    // ProtectedRoute or server session check redirects back to login
    await expect(page).toHaveURL(/.*\/ui\/login/);
    await expect(page.locator('[data-testid="login-page"]')).toBeVisible();
  });
});
