/* global process, console */
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.resolve(__dirname, "../dist");

export const BUDGETS = {
  // Initial bundle / entry point
  entryJsMaxBytes: 480 * 1024, // 480 KiB uncompressed
  entryJsMaxGzipBytes: 150 * 1024, // 150 KiB gzip
  // Individual route chunk budgets
  routeOverviewMaxBytes: 50 * 1024,
  routeKeysMaxBytes: 50 * 1024,
  routeRequestsMaxBytes: 80 * 1024,
  routeSystemMaxBytes: 50 * 1024,
  routeLoginMaxBytes: 30 * 1024,
  routeUsageMaxBytes: 380 * 1024, // Usage includes Recharts
  // CSS budget
  totalCssMaxBytes: 100 * 1024,
  // Total JS assets
  totalJsMaxBytes: 1200 * 1024,
};

export function checkBudgets() {
  if (!fs.existsSync(distDir)) {
    throw new Error(`Dist directory not found: ${distDir}. Run 'npm run build' first.`);
  }

  const assetsDir = path.join(distDir, "assets");
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
  const indexHtml = fs.readFileSync(path.join(distDir, "index.html"), "utf-8");
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

  // Check chunk splitting: must have at least 5 separate JS chunks (app + lazy routes)
  if (stats.js.length < 5) {
    stats.failures.push(
      `Expected at least 5 separate JS chunks for route lazy-loading, found ${stats.js.length}`
    );
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
