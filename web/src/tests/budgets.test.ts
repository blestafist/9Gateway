import { describe, it, expect } from "vitest";
// @ts-expect-error JS script module without typings
import { checkBudgets, BUDGETS } from "../../scripts/budget-check.js";
// @ts-expect-error JS script module without typings
import { generateDependencyReport } from "../../scripts/dependency-report.js";

describe("Production Bundle & Architecture Budgets (T179)", () => {
  it("enforces production bundle size, chunk count, and css budgets", () => {
    const result = checkBudgets();
    expect(result.failures).toHaveLength(0);
    expect(result.js.length).toBeGreaterThanOrEqual(5);
    expect(result.totalCssBytes).toBeLessThanOrEqual(BUDGETS.totalCssMaxBytes);
    expect(result.totalJsBytes).toBeLessThanOrEqual(BUDGETS.totalJsMaxBytes);
  });

  it("verifies architectural import boundaries and reports pervasive dependencies", () => {
    const report = generateDependencyReport();
    expect(report.boundaryViolations).toHaveLength(0);
    expect(report.routeFeatures).toContain("overview");
    expect(report.routeFeatures).toContain("usage");
    expect(report.routeFeatures).toContain("keys");
    expect(report.routeFeatures).toContain("requests");
    expect(report.routeFeatures).toContain("system");
    expect(report.routeFeatures).toContain("auth");

    // Ensure pervasive modules are tracked
    const pervasiveNames = report.pervasiveModules.map(([name]: [string, unknown]) => name);
    expect(pervasiveNames).toContain("shared/transport");
  });
});
