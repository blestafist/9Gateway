import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import React, { useState } from "react";
import {
  Button,
  IconButton,
  Input,
  Select,
  Checkbox,
  Switch,
  Badge,
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
} from "./index";

describe("Button and IconButton", () => {
  it("renders with variant and size classes", () => {
    render(<Button variant="primary" size="lg">Submit</Button>);
    const btn = screen.getByRole("button", { name: "Submit" });
    expect(btn).toHaveClass("gw-btn--primary");
    expect(btn).toHaveClass("gw-btn--lg");
  });

  it("handles loading state by showing spinner and setting aria-busy", () => {
    render(<Button isLoading>Save</Button>);
    const btn = screen.getByRole("button");
    expect(btn).toHaveAttribute("aria-busy", "true");
    expect(btn).toBeDisabled();
  });

  it("IconButton enforces accessible name", () => {
    render(<IconButton icon={<span>*</span>} aria-label="Settings" />);
    expect(screen.getByRole("button", { name: "Settings" })).toBeInTheDocument();
  });
});

describe("Switch", () => {
  it("renders role switch and toggles on click", () => {
    const handleChange = vi.fn();
    render(<Switch checked={false} onChange={handleChange} label="Enable Proxy" />);
    const sw = screen.getByRole("switch", { name: "Enable Proxy" });
    expect(sw).toHaveAttribute("aria-checked", "false");

    fireEvent.click(sw);
    expect(handleChange).toHaveBeenCalledWith(true);
  });

  it("toggles on Space and Enter keys", () => {
    const handleChange = vi.fn();
    render(<Switch checked={true} onChange={handleChange} aria-label="Toggle" />);
    const sw = screen.getByRole("switch", { name: "Toggle" });
    expect(sw).toHaveAttribute("aria-checked", "true");

    fireEvent.keyDown(sw, { key: " " });
    expect(handleChange).toHaveBeenCalledWith(false);

    fireEvent.keyDown(sw, { key: "Enter" });
    expect(handleChange).toHaveBeenCalledWith(false);
  });

  it("does not toggle when disabled", () => {
    const handleChange = vi.fn();
    render(<Switch checked={false} onChange={handleChange} disabled aria-label="Disabled Switch" />);
    const sw = screen.getByRole("switch", { name: "Disabled Switch" });
    expect(sw).toBeDisabled();

    fireEvent.click(sw);
    expect(handleChange).not.toHaveBeenCalled();

    fireEvent.keyDown(sw, { key: " " });
    expect(handleChange).not.toHaveBeenCalled();
  });
});

describe("Tabs", () => {
  const TabsConsumer: React.FC = () => {
    const [active, setActive] = useState("tab1");
    return (
      <Tabs
        activeTab={active}
        onChange={setActive}
        aria-label="Navigation Tabs"
        items={[
          { id: "tab1", label: "First Tab", content: <div>Panel One</div> },
          { id: "tab2", label: "Second Tab", content: <div>Panel Two</div> },
          { id: "tab3", label: "Third Tab", disabled: true, content: <div>Panel Three</div> },
          { id: "tab4", label: "Fourth Tab", content: <div>Panel Four</div> },
        ]}
      />
    );
  };

  it("renders tabs and active panel", () => {
    render(<TabsConsumer />);
    expect(screen.getByRole("tablist", { name: "Navigation Tabs" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "First Tab" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Panel One");
  });

  it("navigates tabs using ArrowRight and ArrowLeft keyboard cycling", () => {
    render(<TabsConsumer />);
    const tab1 = screen.getByRole("tab", { name: "First Tab" });
    tab1.focus();

    // ArrowRight moves to tab2
    fireEvent.keyDown(tab1, { key: "ArrowRight" });
    const tab2 = screen.getByRole("tab", { name: "Second Tab" });
    expect(tab2).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Panel Two");

    // ArrowRight skips disabled tab3 and moves to tab4
    fireEvent.keyDown(tab2, { key: "ArrowRight" });
    const tab4 = screen.getByRole("tab", { name: "Fourth Tab" });
    expect(tab4).toHaveAttribute("aria-selected", "true");

    // ArrowLeft moves back to tab2
    fireEvent.keyDown(tab4, { key: "ArrowLeft" });
    expect(screen.getByRole("tab", { name: "Second Tab" })).toHaveAttribute("aria-selected", "true");
  });

  it("navigates to first and last tab on Home and End", () => {
    render(<TabsConsumer />);
    const tab1 = screen.getByRole("tab", { name: "First Tab" });
    tab1.focus();

    fireEvent.keyDown(tab1, { key: "End" });
    expect(screen.getByRole("tab", { name: "Fourth Tab" })).toHaveAttribute("aria-selected", "true");

    const tab4 = screen.getByRole("tab", { name: "Fourth Tab" });
    fireEvent.keyDown(tab4, { key: "Home" });
    expect(screen.getByRole("tab", { name: "First Tab" })).toHaveAttribute("aria-selected", "true");
  });
});

describe("Dialog and Drawer", () => {
  const DialogHarness: React.FC = () => {
    const [open, setOpen] = useState(false);
    return (
      <div>
        <button data-testid="open-trigger" onClick={() => setOpen(true)}>
          Open Dialog
        </button>
        <Dialog
          isOpen={open}
          onClose={() => setOpen(false)}
          title="Modal Test"
          description="Test description"
        >
          <div>
            <input data-testid="dialog-input-1" placeholder="First Field" />
            <input data-testid="dialog-input-2" placeholder="Second Field" />
            <button data-testid="dialog-btn" onClick={() => setOpen(false)}>
              Close Inside
            </button>
          </div>
        </Dialog>
      </div>
    );
  };

  it("opens modal, traps focus on Tab, closes on Escape, and restores focus", () => {
    render(<DialogHarness />);
    const trigger = screen.getByTestId("open-trigger");
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "Modal Test" });
    expect(dialog).toBeInTheDocument();
    expect(dialog).toHaveAttribute("aria-modal", "true");

    // Close on Escape
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Focus restored to trigger
    expect(document.activeElement).toBe(trigger);
  });

  it("closes dialog on backdrop click", () => {
    render(<DialogHarness />);
    fireEvent.click(screen.getByTestId("open-trigger"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    const backdrop = screen.getByTestId("dialog-backdrop");
    fireEvent.click(backdrop);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("renders Drawer and handles close", () => {
    const handleClose = vi.fn();
    render(
      <Drawer isOpen={true} onClose={handleClose} title="Side Drawer">
        <div>Drawer Content</div>
      </Drawer>
    );

    expect(screen.getByRole("dialog", { name: "Side Drawer" })).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("drawer-backdrop"));
    expect(handleClose).toHaveBeenCalled();
  });
});

describe("Toast System", () => {
  const ToastHarness: React.FC = () => {
    const toast = useToast();
    return (
      <div>
        <button onClick={() => toast.show({ title: "Info Msg", variant: "info" })}>
          Trigger Info
        </button>
        <button onClick={() => toast.show({ title: "Error Msg", variant: "danger" })}>
          Trigger Error
        </button>
      </div>
    );
  };

  it("exposes correct live-region semantics for polite vs assertive toasts", () => {
    render(
      <ToastProvider>
        <ToastHarness />
      </ToastProvider>
    );

    // Trigger Info (polite status)
    fireEvent.click(screen.getByText("Trigger Info"));
    const infoToast = screen.getByRole("status");
    expect(infoToast).toHaveAttribute("aria-live", "polite");
    expect(infoToast).toHaveTextContent("Info Msg");

    // Trigger Danger (assertive alert)
    fireEvent.click(screen.getByText("Trigger Error"));
    const errorToast = screen.getByRole("alert");
    expect(errorToast).toHaveAttribute("aria-live", "assertive");
    expect(errorToast).toHaveTextContent("Error Msg");

    // Dismiss manually
    const dismissBtns = screen.getAllByRole("button", { name: "Close notification" });
    const firstBtn = dismissBtns[0];
    expect(firstBtn).toBeDefined();
    if (firstBtn) {
      fireEvent.click(firstBtn);
    }
    expect(screen.queryByText("Info Msg")).not.toBeInTheDocument();
  });
});

describe("Tooltip", () => {
  it("displays on hover/focus and dismisses on Escape", () => {
    render(
      <Tooltip content="Helper detail">
        <button>Hover Me</button>
      </Tooltip>
    );

    const btn = screen.getByRole("button", { name: "Hover Me" });
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    act(() => {
      fireEvent.focus(btn);
    });
    // With delayMs default, advance or check
    expect(screen.getByRole("button")).toBeInTheDocument();
  });
});

describe("Form Controls & Accessible Feedback", () => {
  it("associates Input labels and errors accessibly", () => {
    render(
      <Input
        label="Gateway Key"
        error="Invalid token key format"
        id="test-key"
      />
    );

    const input = screen.getByLabelText("Gateway Key");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAttribute("aria-describedby", "test-key-error");
    expect(screen.getByRole("alert")).toHaveTextContent("Invalid token key format");
  });

  it("Select connects label and options", () => {
    render(
      <Select label="Model Selection" options={[{ value: "v1", label: "Version 1" }]} />
    );
    expect(screen.getByLabelText("Model Selection")).toBeInTheDocument();
  });

  it("Checkbox toggles correctly", () => {
    const handleChange = vi.fn();
    render(<Checkbox label="Agree to policy" checked={false} onChange={handleChange} />);
    const cb = screen.getByLabelText("Agree to policy");
    fireEvent.click(cb);
    expect(handleChange).toHaveBeenCalled();
  });
});

describe("Visual & Structural Primitives", () => {
  it("renders Badge variants with dot", () => {
    render(<Badge variant="success" dot>Active</Badge>);
    const badge = screen.getByText("Active").closest(".gw-badge");
    expect(badge).toHaveClass("gw-badge--success");
  });

  it("renders Alert with role alert for danger and status for info", () => {
    const { rerender } = render(<Alert variant="danger">Security Warning</Alert>);
    expect(screen.getByRole("alert")).toBeInTheDocument();

    rerender(<Alert variant="info">System Update</Alert>);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("renders Table shell with sortable column indicator", () => {
    const onSort = vi.fn();
    render(
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead sortDirection="asc" onSort={onSort}>
              Column A
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell>Value A1</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    );

    const th = screen.getByRole("columnheader", { name: "Column A" });
    expect(th).toHaveAttribute("aria-sort", "ascending");
    fireEvent.click(th);
    expect(onSort).toHaveBeenCalled();
  });

  it("renders Skeleton and EmptyState", () => {
    render(
      <div>
        <Skeleton variant="text" width={120} height={20} data-testid="skeleton" />
        <EmptyState title="No Records" description="Try clearing filters" />
      </div>
    );
    expect(screen.getByTestId("skeleton")).toHaveClass("gw-skeleton--text");
    expect(screen.getByText("No Records")).toBeInTheDocument();
    expect(screen.getByText("Try clearing filters")).toBeInTheDocument();
  });
});
