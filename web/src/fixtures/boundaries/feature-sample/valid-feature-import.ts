// Boundary Fixture: Feature importing public entry point of another feature and shared primitives.
// This conforms to all boundary rules.
import { SmokeStatus } from "../../../features/smoke";
import { StatusPill } from "../../../shared";

export const testValidFeatureImport = {
  SmokeStatus,
  StatusPill,
};
