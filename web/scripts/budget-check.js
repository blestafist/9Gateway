/* global process, console */
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.resolve(__dirname, "../dist");

export const ROUTE_BUDGETS = {
  overview: { source: "src/features/overview/index.ts", maxBytes: 50 * 1024 },
  keys: { source: "src/features/keys/index.ts", maxBytes: 70 * 1024 },
  requests: { source: "src/features/requests/index.ts", maxBytes: 80 * 1024 },
  system: { source: "src/features/system/index.ts", maxBytes: 50 * 1024 },
  login: { source: "src/features/auth/index.ts", maxBytes: 30 * 1024 },
  usage: { source: "src/features/usage/index.ts", maxBytes: 440 * 1024 },
};

export const BUDGETS = {
  // Initial bundle / entry point
  entryJsMaxBytes: 480 * 1024, // 480 KiB uncompressed
  entryJsMaxGzipBytes: 150 * 1024, // 150 KiB gzip
  // Individual route chunk budgets
  routeOverviewMaxBytes: 50 * 1024,
  routeKeysMaxBytes: 70 * 1024,
  routeRequestsMaxBytes: 80 * 1024,
  routeSystemMaxBytes: 50 * 1024,
  routeLoginMaxBytes: 30 * 1024,
  routeUsageMaxBytes: 440 * 1024, // Usage includes Recharts
  // CSS budget
  totalCssMaxBytes: 100 * 1024,
  // Total JS assets
  totalJsMaxBytes: 1200 * 1024,
};

export function resolveRouteChunks(manifest) {
  return Object.fromEntries(
    Object.entries(ROUTE_BUDGETS).map(([route, budget]) => {
      const matches = Object.entries(manifest).filter(([, entry]) => entry.src === budget.source);
      return [route, matches.length === 1 ? path.basename(matches[0][1].file) : null];
    })
  );
}

export function checkBudgets(outputDir = distDir) {
  if (!fs.existsSync(outputDir)) {
    throw new Error(`Dist directory not found: ${outputDir}. Run 'npm run build' first.`);
  }

  const assetsDir = path.join(outputDir, "assets");
  if (!fs.existsSync(assetsDir)) {
    throw new Error(`Assets directory not found: ${assetsDir}`);
  }

  const files = fs.readdirSync(assetsDir);
  const jsFiles = files.filter((f) => f.endsWith(".js"));
  const cssFiles = files.filter((f) => f.endsWith(".css"));

  const stats = {
    js: [],
    css: [],
    totalJsBytes: 0,
    totalCssBytes: 0,
    failures: [],
  };

  for (const f of jsFiles) {
    const fullPath = path.join(assetsDir, f);
    const content = fs.readFileSync(fullPath);
    const uncompressed = content.length;
    const gzipped = zlib.gzipSync(content).length;
    stats.js.push({ name: f, uncompressed, gzipped });
    stats.totalJsBytes += uncompressed;
  }

  for (const f of cssFiles) {
    const fullPath = path.join(assetsDir, f);
    const content = fs.readFileSync(fullPath);
    const uncompressed = content.length;
    const gzipped = zlib.gzipSync(content).length;
    stats.css.push({ name: f, uncompressed, gzipped });
    stats.totalCssBytes += uncompressed;
  }

  // Find entry JS
  const indexHtml = fs.readFileSync(path.join(outputDir, "index.html"), "utf-8");
  const entryMatch = indexHtml.match(/src="\/ui\/assets\/([^"]+\.js)"/);
  const entryFileName = entryMatch ? entryMatch[1] : null;

  if (!entryFileName) {
    stats.failures.push("Could not determine entry point JS from dist/index.html");
  } else {
    const entryStat = stats.js.find((j) => j.name === entryFileName);
    if (entryStat) {
      if (entryStat.uncompressed > BUDGETS.entryJsMaxBytes) {
        stats.failures.push(
          `Entry JS ${entryFileName} uncompressed size ${entryStat.uncompressed} B exceeds limit ${BUDGETS.entryJsMaxBytes} B`
        );
      }
      if (entryStat.gzipped > BUDGETS.entryJsMaxGzipBytes) {
        stats.failures.push(
          `Entry JS ${entryFileName} gzip size ${entryStat.gzipped} B exceeds limit ${BUDGETS.entryJsMaxGzipBytes} B`
        );
      }
    }
  }

  // Check total CSS
  if (stats.totalCssBytes > BUDGETS.totalCssMaxBytes) {
    stats.failures.push(
      `Total CSS size ${stats.totalCssBytes} B exceeds limit ${BUDGETS.totalCssMaxBytes} B`
    );
  }

  // Check total JS
  if (stats.totalJsBytes > BUDGETS.totalJsMaxBytes) {
    stats.failures.push(
      `Total JS size ${stats.totalJsBytes} B exceeds limit ${BUDGETS.totalJsMaxBytes} B`
    );
  }

  // Enforce every declared lazy route against Vite's manifest. A missing or
  // ambiguous manifest entry is a failure, rather than silently skipping a budget.
  const manifestPath = path.join(outputDir, ".vite", "manifest.json");
  if (!fs.existsSync(manifestPath)) {
    stats.failures.push(`Vite manifest not found: ${manifestPath}`);
  } else {
    let manifest;
    try {
      manifest = JSON.parse(fs.readFileSync(manifestPath, "utf-8"));
    } catch (error) {
      stats.failures.push(`Could not parse Vite manifest: ${error.message}`);
      manifest = null;
    }
    if (manifest) {
      for (const [route, budget] of Object.entries(ROUTE_BUDGETS)) {
        const fileName = resolveRouteChunks(manifest)[route];
        if (!fileName) {
          const matches = Object.entries(manifest).filter(([, entry]) => entry.src === budget.source);
          stats.failures.push(
            `Could not identify exactly one manifest chunk for lazy route ${route} (${budget.source}); found ${matches.length}`
          );
          continue;
        }
        const routeStat = stats.js.find((item) => item.name === fileName);
        if (!routeStat) {
          stats.failures.push(`Manifest chunk for lazy route ${route} is missing from assets: ${fileName}`);
        } else if (routeStat.uncompressed > budget.maxBytes) {
          stats.failures.push(
            `Route ${route} chunk ${fileName} uncompressed size ${routeStat.uncompressed} B exceeds limit ${budget.maxBytes} B`
          );
        }
      }
    }
  }

  return stats;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    const result = checkBudgets();
    console.log("=== 9Gateway Production Bundle Budget Check ===");
    console.log(`Total JS Chunks: ${result.js.length} (${(result.totalJsBytes / 1024).toFixed(1)} KiB)`);
    console.log(`Total CSS: ${result.css.length} files (${(result.totalCssBytes / 1024).toFixed(1)} KiB)`);
    for (const j of result.js) {
      console.log(`  - ${j.name}: ${(j.uncompressed / 1024).toFixed(1)} KiB (gzip: ${(j.gzipped / 1024).toFixed(1)} KiB)`);
    }
    if (result.failures.length > 0) {
      console.error("\nBUDGET VIOLATIONS:");
      for (const err of result.failures) {
        console.error(`  - ❌ ${err}`);
      }
      process.exit(1);
    } else {
      console.log("\n✅ All production bundle budgets passed successfully.");
    }
  } catch (err) {
    console.error("Budget check error:", err.message);
    process.exit(1);
  }
}
