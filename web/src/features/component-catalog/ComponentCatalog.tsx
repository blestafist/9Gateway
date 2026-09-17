import React, { useState } from "react";
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
  CardDescription,
  CardContent,
  CardFooter,
  Tabs,
  Tooltip,
  Dialog,
  Drawer,
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  Skeleton,
  EmptyState,
  Alert,
  ToastProvider,
  useToast,
} from "../../shared/ui";
import { ThemeProvider, useTheme } from "../../shared/theme";
import {
  Sun,
  Moon,
  Search,
  Key,
  Copy,
  Check,
  Eye,
  EyeOff,
  Inbox,
  Sparkles,
  RefreshCw,
  Terminal,
} from "lucide-react";

const CatalogInner: React.FC = () => {
  const { theme, toggleTheme } = useTheme();
  const toast = useToast();

  // State for interactive demos
  const [switchChecked, setSwitchChecked] = useState(true);
  const [checkboxChecked, setCheckboxChecked] = useState(true);
  const [activeSegmentTab, setActiveSegmentTab] = useState("overview");
  const [activeLineTab, setActiveLineTab] = useState("logs");
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [isDrawerOpen, setIsDrawerOpen] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [copiedKey, setCopiedKey] = useState(false);
  const [sortDirection, setSortDirection] = useState<"asc" | "desc" | null>("asc");

  const handleCopy = () => {
    setCopiedKey(true);
    toast.show({
      title: "Key Copied",
      description: "API key token sk-c47...2494 copied to clipboard",
      variant: "success",
    });
    setTimeout(() => setCopiedKey(false), 2000);
  };

  const handleSortToggle = () => {
    if (sortDirection === "asc") setSortDirection("desc");
    else if (sortDirection === "desc") setSortDirection(null);
    else setSortDirection("asc");
  };

  return (
    <div
      className="bg-grid-canvas"
      style={{
        minHeight: "100vh",
        padding: "2rem 1.5rem",
        color: "var(--text-primary)",
      }}
    >
      <div style={{ maxWidth: "1100px", margin: "0 auto", display: "flex", flexDirection: "column", gap: "2.5rem" }}>
        {/* Header Bar */}
        <header
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            flexWrap: "wrap",
            gap: "1rem",
            paddingBottom: "1.5rem",
            borderBottom: "1px solid var(--border-default)",
          }}
        >
          <div>
            <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
              <div
                style={{
                  width: "12px",
                  height: "12px",
                  borderRadius: "var(--radius-full)",
                  backgroundColor: "var(--accent-primary)",
                }}
              />
              <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 700 }}>
                9Gateway Design System &amp; Primitives Catalog
              </h1>
            </div>
            <p style={{ fontSize: "var(--font-size-sm)", color: "var(--text-secondary)", marginTop: "0.25rem" }}>
              Development showcase verifying tokens, states, accessibility, and high-density dark/light themes.
            </p>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
            <Button
              variant="outline"
              size="sm"
              leftIcon={theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
              onClick={toggleTheme}
              aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
            >
              {theme === "dark" ? "Light Mode" : "Dark Mode"}
            </Button>
            <Badge variant="neutral" size="sm">
              Mode: {theme.toUpperCase()}
            </Badge>
          </div>
        </header>

        {/* Section 1: Buttons & IconButtons */}
        <Card>
          <CardHeader>
            <CardTitle>1. Buttons &amp; IconButtons</CardTitle>
            <CardDescription>
              Variants, sizes, loading spinners, disabled states, and keyboard focus outlines.
            </CardDescription>
          </CardHeader>
          <CardContent style={{ display: "flex", flexDirection: "column", gap: "1.25rem" }}>
            <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem", alignItems: "center" }}>
              <Button variant="primary">Primary Coral</Button>
              <Button variant="secondary">Secondary Slate</Button>
              <Button variant="outline">Outline</Button>
              <Button variant="ghost">Ghost</Button>
              <Button variant="danger">Danger</Button>
              <Button variant="primary" isLoading>
                Loading
              </Button>
              <Button variant="secondary" disabled>
                Disabled
              </Button>
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem", alignItems: "center" }}>
              <Button variant="primary" size="sm">Small (32px)</Button>
              <Button variant="primary" size="md">Medium (40px)</Button>
              <Button variant="primary" size="lg">Large (48px)</Button>
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem", alignItems: "center" }}>
              <IconButton icon={<Search size={16} />} aria-label="Search items" variant="outline" size="sm" />
              <IconButton icon={<Key size={18} />} aria-label="Inspect API keys" variant="secondary" size="md" />
              <IconButton icon={<Sparkles size={20} />} aria-label="AI generation" variant="primary" size="lg" />
              <IconButton icon={<RefreshCw size={18} />} aria-label="Refreshing..." isLoading size="md" />
              <IconButton icon={<Search size={18} />} aria-label="Disabled search" disabled size="md" />
            </div>
          </CardContent>
        </Card>

        {/* Section 2: Form Controls */}
        <Card>
          <CardHeader>
            <CardTitle>2. Form Controls &amp; Toggles</CardTitle>
            <CardDescription>
              Inputs with icons and actions, custom select, accessible checkbox, and warm-coral toggle switch.
            </CardDescription>
          </CardHeader>
          <CardContent style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))", gap: "1.5rem" }}>
            <Input
              label="Standard Input"
              placeholder="e.g. gpt-4o-mini"
              helperText="Target upstream model alias"
            />
            <Input
              label="Search Input"
              leftIcon={<Search size={16} />}
              placeholder="Search request logs..."
            />
            <Input
              label="Secret API Key (with reveal/copy)"
              type={showPassword ? "text" : "password"}
              defaultValue="sk-c478901234567890abcdef"
              actionIcon={
                showPassword ? <EyeOff size={16} /> : <Eye size={16} />
              }
              actionLabel={showPassword ? "Hide secret" : "Reveal secret"}
              onActionClick={() => setShowPassword(!showPassword)}
            />
            <Input
              label="Invalid Field"
              defaultValue="malformed-url-format"
              error="Must be a valid HTTP or HTTPS upstream endpoint"
            />
            <Input
              label="Disabled Control"
              defaultValue="Read-only system value"
              disabled
            />
            <Select
              label="Proxy Upstream Policy"
              defaultValue="round-robin"
              options={[
                { value: "round-robin", label: "Round Robin Fallback" },
                { value: "least-latency", label: "Lowest Latency First" },
                { value: "cost-priority", label: "Cost-Optimized Routing" },
              ]}
              helperText="Governed upstream selection strategy"
            />
            <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
              <span className="gw-form-label">Toggles &amp; Checkboxes</span>
              <Switch
                label="Require API Key"
                description="Reject incoming proxy requests without sk-* bearer token"
                checked={switchChecked}
                onChange={setSwitchChecked}
              />
              <Checkbox
                label="Enable Stream Aggregation"
                description="Buffer SSE events into unified JSON when stream: false"
                checked={checkboxChecked}
                onChange={(e) => setCheckboxChecked(e.target.checked)}
              />
              <Checkbox
                label="Disabled Governance Check"
                description="System policy cannot be overridden in local mode"
                disabled
                checked
              />
            </div>
          </CardContent>
        </Card>

        {/* Section 3: Telemetry KPI Cards (Reference 1.png) */}
        <div>
          <div style={{ marginBottom: "1rem" }}>
            <h2 style={{ fontSize: "var(--font-size-md)", fontWeight: 600, color: "var(--text-primary)" }}>
              3. Telemetry KPI Cards (Observed from Reference 1.png)
            </h2>
            <p style={{ fontSize: "var(--font-size-sm)", color: "var(--text-secondary)" }}>
              Distinct metric roles: Neutral requests, coral input tokens, blue cached tokens, green output, gold cost.
            </p>
          </div>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(190px, 1fr))",
              gap: "1rem",
            }}
          >
            <Card>
              <CardHeader style={{ padding: "0.875rem 1rem 0.25rem" }}>
                <span style={{ fontSize: "var(--font-size-xs)", fontWeight: 600, color: "var(--text-muted)", textTransform: "uppercase" }}>
                  Total Requests
                </span>
              </CardHeader>
              <CardContent style={{ padding: "0.25rem 1rem 0.875rem" }}>
                <div style={{ fontSize: "var(--font-size-2xl)", fontWeight: 700, color: "var(--metric-requests)", fontFamily: "var(--font-mono)" }}>
                  150
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader style={{ padding: "0.875rem 1rem 0.25rem" }}>
                <span style={{ fontSize: "var(--font-size-xs)", fontWeight: 600, color: "var(--text-muted)", textTransform: "uppercase" }}>
                  Total Input Tokens
                </span>
              </CardHeader>
              <CardContent style={{ padding: "0.25rem 1rem 0.875rem" }}>
                <div style={{ fontSize: "var(--font-size-2xl)", fontWeight: 700, color: "var(--metric-tokens-in)", fontFamily: "var(--font-mono)" }}>
                  19,178,344
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader style={{ padding: "0.875rem 1rem 0.25rem" }}>
                <span style={{ fontSize: "var(--font-size-xs)", fontWeight: 600, color: "var(--text-muted)", textTransform: "uppercase" }}>
                  Cached Tokens
                </span>
              </CardHeader>
              <CardContent style={{ padding: "0.25rem 1rem 0.875rem" }}>
                <div style={{ fontSize: "var(--font-size-2xl)", fontWeight: 700, color: "var(--metric-tokens-cache)", fontFamily: "var(--font-mono)" }}>
                  11,381,601
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader style={{ padding: "0.875rem 1rem 0.25rem" }}>
                <span style={{ fontSize: "var(--font-size-xs)", fontWeight: 600, color: "var(--text-muted)", textTransform: "uppercase" }}>
                  Output Tokens
                </span>
              </CardHeader>
              <CardContent style={{ padding: "0.25rem 1rem 0.875rem" }}>
                <div style={{ fontSize: "var(--font-size-2xl)", fontWeight: 700, color: "var(--metric-tokens-out)", fontFamily: "var(--font-mono)" }}>
                  72,803
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader style={{ padding: "0.875rem 1rem 0.25rem" }}>
                <span style={{ fontSize: "var(--font-size-xs)", fontWeight: 600, color: "var(--text-muted)", textTransform: "uppercase" }}>
                  Est. Cost
                </span>
              </CardHeader>
              <CardContent style={{ padding: "0.25rem 1rem 0.875rem" }}>
                <div style={{ fontSize: "var(--font-size-2xl)", fontWeight: 700, color: "var(--metric-cost)", fontFamily: "var(--font-mono)" }}>
                  ~$27.99
                </div>
              </CardContent>
            </Card>
          </div>
        </div>

        {/* Section 4: Tabs & Navigation */}
        <Card>
          <CardHeader>
            <CardTitle>4. Tabs &amp; Segmented Controls</CardTitle>
            <CardDescription>
              WAI-ARIA accessible segmented tabs and line tabs with arrow key cycling.
            </CardDescription>
          </CardHeader>
          <CardContent style={{ display: "flex", flexDirection: "column", gap: "1.5rem" }}>
            <div>
              <div style={{ marginBottom: "0.5rem", fontSize: "var(--font-size-sm)", color: "var(--text-secondary)" }}>
                Segmented Pills (Reference 1.png style):
              </div>
              <Tabs
                variant="segmented"
                activeTab={activeSegmentTab}
                onChange={setActiveSegmentTab}
                items={[
                  { id: "overview", label: "Overview", content: <p>Overview tab content panel.</p> },
                  { id: "details", label: "Details", content: <p>Detailed proxy breakdown panel.</p> },
                  { id: "tokens", label: "Tokens", content: <p>Token utilization histogram panel.</p> },
                  { id: "disabled", label: "Disabled", disabled: true },
                ]}
              />
            </div>
            <div>
              <div style={{ marginBottom: "0.5rem", fontSize: "var(--font-size-sm)", color: "var(--text-secondary)" }}>
                Line Tabs:
              </div>
              <Tabs
                variant="line"
                activeTab={activeLineTab}
                onChange={setActiveLineTab}
                items={[
                  { id: "logs", label: "Request Logs", content: <p>Streaming request logs panel.</p> },
                  { id: "keys", label: "Active Keys", content: <p>Managed API keys panel.</p> },
                  { id: "diagnostics", label: "Runtime Diagnostics", content: <p>Diagnostics diagnostics panel.</p> },
                ]}
              />
            </div>
          </CardContent>
        </Card>

        {/* Section 5: Badges & Tooltips */}
        <Card>
          <CardHeader>
            <CardTitle>5. Badges &amp; Tooltips</CardTitle>
            <CardDescription>Status indicators, semantic tags, and keyboard accessible tooltips.</CardDescription>
          </CardHeader>
          <CardContent style={{ display: "flex", flexWrap: "wrap", gap: "1.25rem", alignItems: "center" }}>
            <Badge variant="default">Default</Badge>
            <Badge variant="success" dot>Operational</Badge>
            <Badge variant="warning" dot>Degraded</Badge>
            <Badge variant="danger" dot>Circuit Broken</Badge>
            <Badge variant="info">v0.5.75</Badge>
            <Badge variant="neutral">Inactive</Badge>
            <Badge variant="success" size="sm" dot>200 OK</Badge>
            <Badge variant="danger" size="sm">429 Rate Limit</Badge>

            <Tooltip content="WCAG AA accessible keyboard tooltip" side="top">
              <Button variant="outline" size="sm">Hover or Focus for Tooltip</Button>
            </Tooltip>
          </CardContent>
        </Card>

        {/* Section 6: Dialog & Drawer Modals */}
        <Card>
          <CardHeader>
            <CardTitle>6. Accessible Dialog &amp; Drawer Overlays</CardTitle>
            <CardDescription>
              Focus trap, Escape key dismiss, backdrop click, and focus restoration to trigger.
            </CardDescription>
          </CardHeader>
          <CardContent style={{ display: "flex", gap: "1rem", flexWrap: "wrap" }}>
            <Button variant="primary" onClick={() => setIsDialogOpen(true)}>
              Open Centered Dialog
            </Button>
            <Button variant="secondary" onClick={() => setIsDrawerOpen(true)}>
              Open Right Drawer
            </Button>

            <Dialog
              isOpen={isDialogOpen}
              onClose={() => setIsDialogOpen(false)}
              title="Create API Key"
              description="Provision a new scoped key for OpenAI proxy ingestion."
              footer={
                <>
                  <Button variant="ghost" onClick={() => setIsDialogOpen(false)}>
                    Cancel
                  </Button>
                  <Button
                    variant="primary"
                    onClick={() => {
                      setIsDialogOpen(false);
                      toast.show({ title: "API Key Created", description: "sk-c47... ready to use", variant: "success" });
                    }}
                  >
                    Confirm &amp; Create
                  </Button>
                </>
              }
            >
              <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
                <Input label="Key Name" placeholder="e.g. staging-worker-01" />
                <Select
                  label="Rate Limit Window"
                  options={[
                    { value: "60", label: "60 requests / minute" },
                    { value: "300", label: "300 requests / minute" },
                    { value: "unlimited", label: "Unlimited requests" },
                  ]}
                />
              </div>
            </Dialog>

            <Drawer
              isOpen={isDrawerOpen}
              onClose={() => setIsDrawerOpen(false)}
              title="Request Details"
              description="Inspection of HTTP headers and upstream routing metadata."
              footer={
                <Button variant="outline" onClick={() => setIsDrawerOpen(false)}>
                  Close Drawer
                </Button>
              }
            >
              <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem", fontSize: "var(--font-size-sm)" }}>
                <div>
                  <strong style={{ color: "var(--text-muted)" }}>Request ID:</strong>
                  <div style={{ fontFamily: "var(--font-mono)", marginTop: "2px" }}>req_01HPX792N4K8G</div>
                </div>
                <div>
                  <strong style={{ color: "var(--text-muted)" }}>Upstream Latency:</strong>
                  <div style={{ fontFamily: "var(--font-mono)", marginTop: "2px" }}>184ms</div>
                </div>
                <div>
                  <strong style={{ color: "var(--text-muted)" }}>Model:</strong>
                  <div style={{ fontFamily: "var(--font-mono)", marginTop: "2px" }}>claude-sonnet-5</div>
                </div>
              </div>
            </Drawer>
          </CardContent>
        </Card>

        {/* Section 7: Table Shell */}
        <Card>
          <CardHeader>
            <CardTitle>7. Data Table Shell</CardTitle>
            <CardDescription>
              High-density rows, sortable header columns, monospace figures, and hover highlights.
            </CardDescription>
          </CardHeader>
          <CardContent style={{ padding: 0 }}>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead sortDirection={sortDirection} onSort={handleSortToggle}>
                    Model
                  </TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>In / Out Tokens</TableHead>
                  <TableHead>Est. Cost</TableHead>
                  <TableHead>Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 500 }}>
                    claude-sonnet-5
                  </TableCell>
                  <TableCell>
                    <Badge variant="success" size="sm" dot>200 OK</Badge>
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", color: "var(--metric-tokens-in)" }}>
                    29,609 <span style={{ color: "var(--metric-tokens-out)" }}>↑ 135 ↓</span>
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", color: "var(--metric-cost)" }}>
                    $0.012
                  </TableCell>
                  <TableCell>
                    <IconButton
                      icon={copiedKey ? <Check size={14} color="var(--status-success)" /> : <Copy size={14} />}
                      aria-label="Copy key snippet"
                      size="sm"
                      onClick={handleCopy}
                    />
                  </TableCell>
                </TableRow>
                <TableRow>
                  <TableCell style={{ fontFamily: "var(--font-mono)", fontWeight: 500 }}>
                    gpt-4o-mini
                  </TableCell>
                  <TableCell>
                    <Badge variant="success" size="sm" dot>200 OK</Badge>
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", color: "var(--metric-tokens-in)" }}>
                    4,712 <span style={{ color: "var(--metric-tokens-out)" }}>↑ 5 ↓</span>
                  </TableCell>
                  <TableCell style={{ fontFamily: "var(--font-mono)", color: "var(--metric-cost)" }}>
                    $0.001
                  </TableCell>
                  <TableCell>
                    <IconButton
                      icon={<Copy size={14} />}
                      aria-label="Copy snippet"
                      size="sm"
                      onClick={handleCopy}
                    />
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        {/* Section 8: Skeletons & Empty State */}
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))", gap: "1.5rem" }}>
          <Card>
            <CardHeader>
              <CardTitle>8. Skeletons</CardTitle>
              <CardDescription>Content placeholders with reduced-motion support.</CardDescription>
            </CardHeader>
            <CardContent style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
              <Skeleton variant="text" width="60%" height={16} />
              <Skeleton variant="text" width="90%" height={14} />
              <Skeleton variant="rect" width="100%" height={64} />
              <div style={{ display: "flex", alignItems: "center", gap: "0.75rem", marginTop: "0.5rem" }}>
                <Skeleton variant="circle" width={36} height={36} />
                <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: "0.375rem" }}>
                  <Skeleton variant="text" width="40%" height={14} />
                  <Skeleton variant="text" width="70%" height={12} />
                </div>
            </div>
          </CardContent>
          <CardFooter>
            <Button variant="ghost" size="sm">Reset to defaults</Button>
            <Button variant="primary" size="sm">Apply actions</Button>
          </CardFooter>
        </Card>

          <Card>
            <CardHeader>
              <CardTitle>9. Empty State</CardTitle>
              <CardDescription>Zero-data feedback container.</CardDescription>
            </CardHeader>
            <CardContent>
              <EmptyState
                icon={<Inbox size={32} />}
                title="No Captured Requests"
                description="Requests will appear here once incoming traffic passes through the proxy."
                action={<Button variant="outline" size="sm" leftIcon={<Terminal size={14} />}>Send Test Request</Button>}
              />
            </CardContent>
          </Card>
        </div>

        {/* Section 10: Alerts & Toast Triggers */}
        <Card>
          <CardHeader>
            <CardTitle>10. Alerts &amp; Toast Live-Region Notifications</CardTitle>
            <CardDescription>
              Inline callouts and floating live notifications (assertive for danger, polite for info/success).
            </CardDescription>
          </CardHeader>
          <CardContent style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
            <Alert variant="info" title="Transparent Ingress Active">
              Incoming requests are evaluated against policy rules with sub-millisecond overhead.
            </Alert>
            <Alert variant="warning" title="Budget Threshold Reached">
              API key pestitntKey has utilized 85% of monthly token allocation.
            </Alert>
            <Alert variant="danger" title="Upstream Connection Failure" onClose={() => alert("Dismissed")}>
              9router upstream connection returned 502 Bad Gateway.
            </Alert>
            <Alert variant="success" title="Database Checkpoint Completed">
              SQLite history compacted and retained rows within window.
            </Alert>

            <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap", marginTop: "0.5rem" }}>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  toast.show({
                    title: "Sync Completed",
                    description: "Polite announcement: Metrics aggregated.",
                    variant: "info",
                  })
                }
              >
                Trigger Info Toast (Polite)
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  toast.show({
                    title: "Action Saved",
                    description: "Polite announcement: Key regenerated.",
                    variant: "success",
                  })
                }
              >
                Trigger Success Toast (Polite)
              </Button>
              <Button
                variant="danger"
                size="sm"
                onClick={() =>
                  toast.show({
                    title: "Rate Limit Exceeded",
                    description: "Assertive alert: Upstream blocked request.",
                    variant: "danger",
                  })
                }
              >
                Trigger Error Toast (Assertive)
              </Button>
            </div>
          </CardContent>
        </Card>

        {/* Section 11: Long Text & Edge Cases */}
        <Card>
          <CardHeader>
            <CardTitle>11. Text Overflow &amp; Edge Cases</CardTitle>
            <CardDescription>
              Verifying 200% text zoom stability, long unbreakable words, and high-density wrapping.
            </CardDescription>
          </CardHeader>
          <CardContent style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
            <div style={{ maxWidth: "340px", border: "1px dashed var(--border-default)", padding: "0.75rem", borderRadius: "var(--radius-sm)" }}>
              <div style={{ fontSize: "var(--font-size-xs)", color: "var(--text-muted)", marginBottom: "4px" }}>
                Bounded 340px Box with Extremely Long String:
              </div>
              <div style={{ wordBreak: "break-all", fontFamily: "var(--font-mono)", fontSize: "var(--font-size-sm)" }}>
                sk-9gw-prod-super-long-token-identifier-with-extraordinary-length-0123456789abcdefghijklmnopqrstuvwxyz
              </div>
              <Button variant="secondary" size="sm" style={{ marginTop: "0.5rem", maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis" }}>
                Button with extremely long label that should not blow up boundaries
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export const ComponentCatalog: React.FC = () => {
  return (
    <ThemeProvider>
      <ToastProvider>
        <CatalogInner />
      </ToastProvider>
    </ThemeProvider>
  );
};

export default ComponentCatalog;
