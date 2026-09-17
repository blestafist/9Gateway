import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import React from "react";
import { ThemeProvider, useTheme, getInitialTheme } from "./theme";

const TestThemeConsumer: React.FC = () => {
  const { theme, toggleTheme, setTheme } = useTheme();
  return (
    <div>
      <span data-testid="current-theme">{theme}</span>
      <button onClick={toggleTheme}>Toggle Theme</button>
      <button onClick={() => setTheme("dark")}>Set Dark</button>
      <button onClick={() => setTheme("light")}>Set Light</button>
    </div>
  );
};

describe("Theme System", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
  });

  it("defaults to dark theme when no storage preference exists", () => {
    expect(getInitialTheme()).toBe("dark");
  });

  it("reads saved preference from localStorage", () => {
    localStorage.setItem("9gateway_theme", "light");
    expect(getInitialTheme()).toBe("light");
  });

  it("updates data-theme attribute on documentElement and persists changes", () => {
    render(
      <ThemeProvider initialTheme="dark">
        <TestThemeConsumer />
      </ThemeProvider>
    );

    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(screen.getByTestId("current-theme")).toHaveTextContent("dark");

    act(() => {
      screen.getByText("Toggle Theme").click();
    });

    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(document.documentElement.style.colorScheme).toBe("light");
    expect(localStorage.getItem("9gateway_theme")).toBe("light");
    expect(screen.getByTestId("current-theme")).toHaveTextContent("light");

    act(() => {
      screen.getByText("Set Dark").click();
    });

    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(localStorage.getItem("9gateway_theme")).toBe("dark");
  });
});
