import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { App } from "./App";

describe("App smoke", () => {
  it("renders 9Gateway header and smoke status", () => {
    render(<App />);
    expect(
      screen.getByRole("heading", { level: 1, name: "9Gateway" })
    ).toBeInTheDocument();
    expect(screen.getByTestId("smoke-status")).toBeInTheDocument();
    expect(screen.getByTestId("status-pill")).toHaveTextContent("operational");
  });
});
