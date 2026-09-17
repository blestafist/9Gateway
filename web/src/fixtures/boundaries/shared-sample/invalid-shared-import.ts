// Boundary Fixture: Shared module reaching into application composition.
// This violates the boundary rule: shared cannot import app composition.
import { App } from "../../../app/App";

export const testBadSharedImport = App;
