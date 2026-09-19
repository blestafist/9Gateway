/* global process, console */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const webSrc = path.resolve(__dirname, "../src");

export function generateDependencyReport() {
  const featuresDir = path.join(webSrc, "features");
  const featureNames = fs
    .readdirSync(featuresDir, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .map((d) => d.name);

  const routeFeatures = ["overview", "usage", "keys", "requests", "system", "auth"];

  const sharedImportCounts = {};
  const featureImports = {};
  const boundaryViolations = [];

  function scanFileImports(filePath) {
    const content = fs.readFileSync(filePath, "utf-8");
    const importRegex = /(?:import|export)\s+(?:.+?\s+from\s+)?["']([^"']+)["']/g;
    const imports = [];
    let match;
    while ((match = importRegex.exec(content)) !== null) {
      imports.push(match[1]);
    }
    return imports;
  }

  function walkDir(dir, fileList = []) {
    if (!fs.existsSync(dir)) return fileList;
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        walkDir(full, fileList);
      } else if (
        (entry.name.endsWith(".ts") || entry.name.endsWith(".tsx")) &&
        !entry.name.includes(".test.")
      ) {
        fileList.push(full);
      }
    }
    return fileList;
  }

  for (const feat of featureNames) {
    featureImports[feat] = new Set();
    const featFiles = walkDir(path.join(featuresDir, feat));
    for (const file of featFiles) {
      const imports = scanFileImports(file);
      for (const imp of imports) {
        // Check illegal cross-feature import
        for (const other of featureNames) {
          if (other !== feat) {
            if (
              imp.includes(`../${other}/`) ||
              imp.includes(`../../features/${other}/`) ||
              imp.startsWith(`@/features/${other}/`)
            ) {
              // Only allow importing index
              if (
                !imp.endsWith(`/${other}`) &&
                !imp.endsWith(`/${other}/index`) &&
                !imp.endsWith(`/${other}/index.ts`) &&
                !imp.endsWith(`/${other}/index.tsx`)
              ) {
                boundaryViolations.push(
                  `Illegal cross-feature private import in ${path.relative(webSrc, file)}: "${imp}"`
                );
              }
            }
          }
        }

        // Shared imports
        const sharedMatch = imp.match(/shared\/([^/]+)/);
        if (sharedMatch) {
          const sharedModule = `shared/${sharedMatch[1]}`;
          featureImports[feat].add(sharedModule);
        }
      }
    }
  }

  // Count shared modules across route features
  for (const route of routeFeatures) {
    const modules = featureImports[route] || new Set();
    for (const mod of modules) {
      sharedImportCounts[mod] = (sharedImportCounts[mod] || 0) + 1;
    }
  }

  const pervasiveModules = Object.entries(sharedImportCounts)
    .filter(([, count]) => count >= 3)
    .sort((a, b) => b[1] - a[1]);

  return {
    routeFeatures,
    sharedImportCounts,
    pervasiveModules,
    boundaryViolations,
  };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    const report = generateDependencyReport();
    console.log("=== 9Gateway Architectural Dependency & Import Boundary Report ===");
    console.log(`Audited Route Features: ${report.routeFeatures.join(", ")}`);
    console.log("\nShared Module Route Adoption:");
    for (const [mod, count] of Object.entries(report.sharedImportCounts)) {
      console.log(`  - ${mod}: imported by ${count}/${report.routeFeatures.length} route features`);
    }

    console.log("\nPervasive Shared Modules (imported by >= 3 routes, track for architectural creep):");
    for (const [mod, count] of report.pervasiveModules) {
      console.log(`  - 📌 ${mod} (${count} routes)`);
    }

    if (report.boundaryViolations.length > 0) {
      console.error("\n❌ BOUNDARY VIOLATIONS DETECTED:");
      for (const v of report.boundaryViolations) {
        console.error(`  - ${v}`);
      }
      process.exit(1);
    } else {
      console.log("\n✅ All import boundaries strictly respected (zero illegal cross-feature private imports).");
    }
  } catch (err) {
    console.error("Dependency report error:", err.message);
    process.exit(1);
  }
}
