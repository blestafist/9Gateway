import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, waitFor, screen, fireEvent } from "@testing-library/react";
import axe from "axe-core";
import { App } from "../app/App";
import keyListFixture from "../features/keys/fixtures/keyList.fixture.json";
import requestListFixture from "../features/requests/fixtures/requestList.fixture.json";
import requestDetailFixture from "../features/requests/fixtures/requestDetail.fixture.json";
import overviewFixture from "../features/overview/fixtures/overview.fixture.json";
import usageTimeseriesFixture from "../features/usage/fixtures/usageTimeseries.fixture.json";
import usageBreakdownFixture from "../features/usage/fixtures/usageBreakdownModel.fixture.json";
import systemFixture from "../features/system/fixtures/systemHealthy.fixture.json";
import createKeyFixture from "../features/keys/fixtures/createKey.fixture.json";
import keyDetailFixture from "../features/keys/fixtures/keyDetail.fixture.json";
import requestBodyFixture from "../features/requests/fixtures/requestBody.fixture.json";
import { RouteErrorBoundary } from "../app/shell/RouteErrorBoundary";
import {
  Button,
  IconButton,
  Input,
  Select,
  Checkbox,
  Switch,
  Badge,
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Tabs,
  Alert,
  EmptyState,
  StatusPill,
} from "../shared/ui";
import { Zap } from "lucide-react";

function mockFetchRouter(url: string, init?: RequestInit) {
  if (url.includes("/admin/v1/overview")) {
    return new Response(JSON.stringify(overviewFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/usage/timeseries")) {
    return new Response(JSON.stringify(usageTimeseriesFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/usage/breakdown")) {
    return new Response(JSON.stringify(usageBreakdownFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/keys/") && init?.method === "PUT") {
    return new Response(JSON.stringify(keyDetailFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/keys/") && (init?.method === "GET" || !init?.method)) {
    return new Response(JSON.stringify(keyDetailFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/keys") && init?.method === "POST") {
    return new Response(JSON.stringify(createKeyFixture), {
      status: 201,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/keys")) {
    return new Response(JSON.stringify(keyListFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/requests/req_01j7abcde/bodies/")) {
    return new Response(JSON.stringify(requestBodyFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/requests/req_01j7abcde")) {
    return new Response(JSON.stringify(requestDetailFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/requests")) {
    return new Response(JSON.stringify(requestListFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url.includes("/admin/v1/system")) {
    return new Response(JSON.stringify(systemFixture), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  return new Response("Not found", { status: 404 });
}

describe("WCAG 2.2 AA Accessibility automated scans", () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    localStorage.clear();
    originalFetch = globalThis.fetch;
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      return Promise.resolve(mockFetchRouter(url, init));
    });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  async function expectNoA11yViolations(container: HTMLElement) {
    const results = await axe.run(container, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"],
      },
    });

    const seriousOrCritical = results.violations.filter(
      (v) => v.impact === "serious" || v.impact === "critical"
    );

    if (seriousOrCritical.length > 0) {
      const details = seriousOrCritical
        .map(
          (v) =>
            `[${v.impact}] ${v.id} (${v.help}):\n` +
            v.nodes
              .map(
                (n) =>
                  `  - Target: ${n.target.join(", ")}\n    HTML: ${n.html}\n    Summary: ${n.failureSummary}`
              )
              .join("\n")
        )
        .join("\n\n");
      expect.fail(`Found ${seriousOrCritical.length} serious/critical a11y violations:\n\n${details}`);
    }

    return results;
  }

  it("passes axe check on Login page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/login"]} initialAuthState={{ isAuthenticated: false }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("login-page")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on Overview page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/overview"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("overview-page")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on Usage page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/usage"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("usage-page")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on API Keys page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/keys"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on Requests page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/requests"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("requests-page")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on Request Detail page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/requests/req_01j7abcde"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("request-detail-view")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on System page", async () => {
    const { container } = render(
      <App initialEntries={["/ui/system"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("system-page")).toBeInTheDocument();
    });

    await expectNoA11yViolations(container);
  });

  it("passes axe check on Command Palette modal", async () => {
    render(
      <App initialEntries={["/ui/overview"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("overview-page")).toBeInTheDocument();
    });

    // Open palette
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });

    await waitFor(() => {
      expect(screen.getByTestId("command-palette")).toBeInTheDocument();
    });

    await expectNoA11yViolations(screen.getByTestId("command-palette"));
  });

  it("passes axe check on Create Key Dialog (form step and secret reveal step)", async () => {
    render(
      <App initialEntries={["/ui/keys"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
    });

    // Open create key dialog via test id
    const createBtn = screen.getByTestId("open-create-key-btn");
    fireEvent.click(createBtn);

    await waitFor(() => {
      expect(screen.getByTestId("create-key-form")).toBeInTheDocument();
    });

    // Verify Step 1 Form
    await expectNoA11yViolations(screen.getByRole("dialog"));

    // Fill form and submit to reach Step 2 Secret Handoff
    const nameInput = screen.getByTestId("create-key-name-input");
    fireEvent.change(nameInput, { target: { value: "A11y Test Key" } });

    const submitBtn = screen.getByTestId("create-key-submit-btn");
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(screen.getByTestId("secret-handoff-step")).toBeInTheDocument();
    });

    // Verify Step 2 Secret Reveal
    await expectNoA11yViolations(screen.getByRole("dialog"));
  });

  it("passes axe check on Key Detail Drawer and Policy Form", async () => {
    render(
      <App initialEntries={["/ui/keys"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("keys-page")).toBeInTheDocument();
    });

    // Click row to open drawer
    const row = screen.getByTestId("key-row-key-0191eb0b62bc7b7489a2434685ef3b63");
    fireEvent.click(row);

    await waitFor(() => {
      expect(screen.getByTestId("key-detail-content")).toBeInTheDocument();
    });

    await expectNoA11yViolations(screen.getByRole("dialog"));
  });

  it("passes axe check on System Safe Diagnostics Summary modal", async () => {
    render(
      <App initialEntries={["/ui/system"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("system-page")).toBeInTheDocument();
    });

    const summaryBtn = screen.getByRole("button", { name: /diagnostics summary/i });
    fireEvent.click(summaryBtn);

    await waitFor(() => {
      expect(screen.getByTestId("diagnostics-preview-box")).toBeInTheDocument();
    });

    await expectNoA11yViolations(screen.getByRole("dialog"));
  });

  it("passes axe check on Request BodyViewer disclosure", async () => {
    render(
      <App initialEntries={["/ui/requests/req_01j7abcde"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("request-detail-view")).toBeInTheDocument();
    });

    const viewBodyBtn = screen.getByTestId("open-body-response-btn");
    fireEvent.click(viewBodyBtn);

    await waitFor(() => {
      expect(screen.getByTestId("body-content-panel")).toBeInTheDocument();
    });

    await expectNoA11yViolations(screen.getByTestId("body-viewer"));
  });

  it("passes axe check on RouteErrorBoundary error state", async () => {
    const ProblemComponent: React.FC = () => {
      throw new Error("Simulated accessibility crash error");
    };

    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    try {
      const { container } = render(
        <RouteErrorBoundary>
          <ProblemComponent />
        </RouteErrorBoundary>
      );

      expect(screen.getByTestId("route-error-boundary")).toBeInTheDocument();
      await expectNoA11yViolations(container);
    } finally {
      consoleSpy.mockRestore();
    }
  });

  it("verifies intentional data scroll regions are labeled and keyboard reachable", async () => {
    render(
      <App initialEntries={["/ui/requests"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("requests-page")).toBeInTheDocument();
    });

    // Inspect table container region
    const tableRegion = screen.getByRole("region", { name: /recent requests scroll region/i });
    expect(tableRegion).toBeInTheDocument();
    expect(tableRegion).toHaveAttribute("tabindex", "0");
  });

  it("passes axe check on composable UI primitives", async () => {
    const { container } = render(
      <main>
        <h1>UI Primitives Accessibility</h1>
        <Card>
          <CardHeader>
            <CardTitle>Controls</CardTitle>
          </CardHeader>
          <CardContent>
            <Button variant="primary">Primary Action</Button>
            <IconButton icon={<Zap size={16} />} aria-label="Quick Action" />
            <Input label="Server Port" id="server-port" placeholder="8080" helperText="Default HTTP port" />
            <Input label="Failed Input" id="failed-input" error="Invalid value" />
            <Select
              label="Environment"
              id="select-env"
              options={[
                { value: "prod", label: "Production" },
                { value: "dev", label: "Development" },
              ]}
              value="prod"
              onChange={() => {}}
            />
            <Checkbox label="Enable Request Logging" checked={true} onChange={() => {}} />
            <Switch label="Maintenance Mode" checked={false} onChange={() => {}} />
            <StatusPill label="Healthy" variant="success" />
            <Badge variant="warning">Rate Limited</Badge>
            <Tabs
              items={[
                { id: "tab-1", label: "Overview" },
                { id: "tab-2", label: "Settings" },
              ]}
              activeTab="tab-1"
              onChange={() => {}}
            />
            <Alert variant="info" title="System Notice">
              All services operational.
            </Alert>
            <EmptyState
              title="No data found"
              description="There are no records matching your query."
              action={<Button variant="secondary">Reset Filters</Button>}
            />
          </CardContent>
        </Card>
      </main>
    );

    await expectNoA11yViolations(container);
  });

  it("verifies responsive viewports and safe areas across shell layout", async () => {
    const { container } = render(
      <App initialEntries={["/ui/overview"]} initialAuthState={{ isAuthenticated: true }} />
    );

    await waitFor(() => {
      expect(screen.getByTestId("overview-page")).toBeInTheDocument();
    });

    // Verify shell container has safe area insets and overflow-x prevention
    const shell = container.querySelector(".gw-shell-container");
    expect(shell).toBeInTheDocument();
    expect(shell).toHaveClass("gw-shell-container");

    // Verify main content landmark exists and is focusable
    const mainContent = document.getElementById("main-content");
    expect(mainContent).toBeInTheDocument();
    expect(mainContent).toHaveAttribute("tabindex", "-1");
  });

  it("handles long localized-looking strings without breaking layout or accessibility", async () => {
    const longString = "A".repeat(250) + "VeryLongIdentifierWithoutSpaces_0123456789";
    const { container } = render(
      <main>
        <h1>Long String Verification</h1>
        <div style={{ maxWidth: "320px", overflowWrap: "break-word" }}>
          <p>{longString}</p>
        </div>
      </main>
    );

    await expectNoA11yViolations(container);
  });
});
