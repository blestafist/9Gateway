import React from "react";
import { BrowserRouter, MemoryRouter } from "react-router-dom";
import { ThemeProvider } from "../shared/theme";
import { AdminQueryProvider } from "../shared/query";
import { AuthProvider } from "../features/auth";
import { AppRoutes } from "./AppRoutes";

export interface AppProps {
  initialEntries?: string[];
  initialAuthState?: {
    isAuthenticated: boolean;
    csrfToken?: string;
    idleExpiresAt?: string;
    expiresAt?: string;
  };
}

export const App: React.FC<AppProps> = ({ initialEntries, initialAuthState }) => {
  const isTestOrNonUiPath =
    typeof window !== "undefined" && !window.location.pathname.startsWith("/ui");

  if (initialEntries && initialEntries.length > 0) {
    return (
      <ThemeProvider>
        <AdminQueryProvider>
          <MemoryRouter initialEntries={initialEntries} basename="/ui">
            <AuthProvider initialAuthState={initialAuthState}>
              <AppRoutes />
            </AuthProvider>
          </MemoryRouter>
        </AdminQueryProvider>
      </ThemeProvider>
    );
  }

  if (isTestOrNonUiPath) {
    return (
      <ThemeProvider>
        <AdminQueryProvider>
          <MemoryRouter initialEntries={["/ui/overview"]} basename="/ui">
            <AuthProvider initialAuthState={initialAuthState}>
              <AppRoutes />
            </AuthProvider>
          </MemoryRouter>
        </AdminQueryProvider>
      </ThemeProvider>
    );
  }

  return (
    <ThemeProvider>
      <AdminQueryProvider>
        <BrowserRouter basename="/ui">
          <AuthProvider initialAuthState={initialAuthState}>
            <AppRoutes />
          </AuthProvider>
        </BrowserRouter>
      </AdminQueryProvider>
    </ThemeProvider>
  );
};

export default App;
