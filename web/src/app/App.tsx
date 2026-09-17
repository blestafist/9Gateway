import React from "react";
import { BrowserRouter, MemoryRouter } from "react-router-dom";
import { ThemeProvider } from "../shared/theme";
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
        <MemoryRouter initialEntries={initialEntries} basename="/ui">
          <AuthProvider initialAuthState={initialAuthState}>
            <AppRoutes />
          </AuthProvider>
        </MemoryRouter>
      </ThemeProvider>
    );
  }

  if (isTestOrNonUiPath) {
    return (
      <ThemeProvider>
        <MemoryRouter initialEntries={["/ui/overview"]} basename="/ui">
          <AuthProvider initialAuthState={initialAuthState}>
            <AppRoutes />
          </AuthProvider>
        </MemoryRouter>
      </ThemeProvider>
    );
  }

  return (
    <ThemeProvider>
      <BrowserRouter basename="/ui">
        <AuthProvider initialAuthState={initialAuthState}>
          <AppRoutes />
        </AuthProvider>
      </BrowserRouter>
    </ThemeProvider>
  );
};

export default App;
