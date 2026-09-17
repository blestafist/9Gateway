import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { SmokeStatus } from "./components/SmokeStatus";

describe("SmokeStatus", () => {
  it("renders the status label", () => {
    render(<SmokeStatus status="active" />);
    expect(screen.getByTestId("smoke-status")).toHaveTextContent(
      "Gateway status:"
    );
    expect(screen.getByTestId("status-pill")).toHaveTextContent("active");
  });
});
