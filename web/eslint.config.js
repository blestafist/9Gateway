import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import boundaries from "eslint-plugin-boundaries";

export default tseslint.config(
  {
    ignores: [
      "dist/**",
      "node_modules/**",
      "src/fixtures/boundaries/**/invalid-*",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    plugins: {
      "react-hooks": reactHooks,
      boundaries,
    },
    settings: {
      "import/resolver": {
        node: {
          extensions: [".js", ".jsx", ".ts", ".tsx"],
        },
      },
      "boundaries/include": ["src/**/*"],
      "boundaries/elements": [
        {
          type: "app",
          pattern: "src/app/**",
        },
        {
          type: "feature",
          pattern: "src/features/*",
          capture: ["featureName"],
        },
        {
          type: "feature",
          pattern: "src/fixtures/boundaries/feature-*",
          capture: ["featureName"],
        },
        {
          type: "shared",
          pattern: "src/shared/**",
        },
        {
          type: "shared",
          pattern: "src/fixtures/boundaries/shared-*",
        },
      ],
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "boundaries/dependencies": [
        "error",
        {
          default: "disallow",
          policies: [
            {
              from: { element: { type: "app" } },
              allow: [
                { to: { element: { type: "feature" } } },
                { to: { element: { type: "shared" } } },
              ],
            },
            {
              from: { element: { type: "feature" } },
              allow: [
                { to: { element: { type: "shared" } } },
                {
                  to: {
                    element: {
                      type: "feature",
                      captured: { featureName: "{{from.featureName}}" },
                    },
                  },
                },
                {
                  to: {
                    element: {
                      type: "feature",
                      fileInternalPath: "index.ts",
                    },
                  },
                },
                {
                  to: {
                    element: {
                      type: "feature",
                      fileInternalPath: "index.tsx",
                    },
                  },
                },
              ],
            },
            {
              from: { element: { type: "shared" } },
              allow: [{ to: { element: { type: "shared" } } }],
            },
          ],
        },
      ],
    },
  }
);
