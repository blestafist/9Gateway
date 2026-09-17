import React from "react";
import { BrowserRouter, MemoryRouter } from "react-router-dom";
import { ThemeProvider } from "../shared/theme";
import { AppRoutes } from "./AppRoutes";

export interface AppProps {
  initialEntries?: string[];
}

export const App: React.FC<AppProps> = ({ initialEntries }) => {
  const isTestOrNonUiPath =
    typeof window !== "undefined" && !window.location.pathname.startsWith("/ui");

  if (initialEntries && initialEntries.length > 0) {
    return (
      <ThemeProvider>
        <MemoryRouter initialEntries={initialEntries} basename="/ui">
          <AppRoutes />
        </MemoryRouter>
      </ThemeProvider>
    );
  }

  if (isTestOrNonUiPath) {
    return (
      <ThemeProvider>
        <MemoryRouter initialEntries={["/ui/overview"]} basename="/ui">
          <AppRoutes />
        </MemoryRouter>
      </ThemeProvider>
    );
  }

  return (
    <ThemeProvider>
      <BrowserRouter basename="/ui">
        <AppRoutes />
      </BrowserRouter>
    </ThemeProvider>
  );
};

export default App;
