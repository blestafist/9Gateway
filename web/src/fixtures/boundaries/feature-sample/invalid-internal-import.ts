// Boundary Fixture: Feature reaching into another feature's internal components.
// This violates the boundary rule: features cannot import another feature's internals.
import { SmokeStatus } from "../../../features/smoke/components/SmokeStatus";

export const testBadFeatureImport = SmokeStatus;
