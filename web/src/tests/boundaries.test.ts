import { describe, it, expect } from "vitest";
import { ESLint } from "eslint";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(__dirname, "../..");

describe("import boundaries", () => {
  const eslint = new ESLint({
    cwd: webRoot,
    ignore: false,
  });

  it("prevents features from importing another feature's internal components", async () => {
    const fixturePath = path.resolve(
      webRoot,
      "src/fixtures/boundaries/feature-sample/invalid-internal-import.ts"
    );
    const [result] = await eslint.lintFiles([fixturePath]);
    expect(result).toBeDefined();
    expect(result?.errorCount).toBeGreaterThan(0);

    const boundaryErrors = result?.messages.filter(
      (m) => m.ruleId === "boundaries/dependencies"
    );
    expect(boundaryErrors?.length).toBeGreaterThan(0);
    expect(boundaryErrors?.[0]?.message).toMatch(
      /There is no policy allowing dependencies from elements of type "feature"/
    );
  });

  it("prevents shared modules from importing application composition", async () => {
    const fixturePath = path.resolve(
      webRoot,
      "src/fixtures/boundaries/shared-sample/invalid-shared-import.ts"
    );
    const [result] = await eslint.lintFiles([fixturePath]);
    expect(result).toBeDefined();
    expect(result?.errorCount).toBeGreaterThan(0);

    const boundaryErrors = result?.messages.filter(
      (m) => m.ruleId === "boundaries/dependencies"
    );
    expect(boundaryErrors?.length).toBeGreaterThan(0);
    expect(boundaryErrors?.[0]?.message).toMatch(
      /There is no policy allowing dependencies from elements of type "shared"/
    );
  });

  it("allows features to import public entry points and shared modules", async () => {
    const fixturePath = path.resolve(
      webRoot,
      "src/fixtures/boundaries/feature-sample/valid-feature-import.ts"
    );
    const [result] = await eslint.lintFiles([fixturePath]);
    expect(result).toBeDefined();
    const boundaryErrors = result?.messages.filter(
      (m) => m.ruleId === "boundaries/dependencies"
    );
    expect(boundaryErrors).toHaveLength(0);
  });
});
