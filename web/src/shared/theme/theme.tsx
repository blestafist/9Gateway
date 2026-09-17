import React, { createContext, useContext, useEffect, useState } from "react";

export type Theme = "dark" | "light";

interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
}

const STORAGE_KEY = "9gateway_theme";

const ThemeContext = createContext<ThemeContextValue | undefined>(undefined);

export function getInitialTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark") {
      return stored;
    }
    if (window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches) {
      return "light";
    }
  } catch {
    // Fallback if localStorage or matchMedia is restricted
  }
  return "dark";
}

export function applyThemeToDOM(theme: Theme): void {
  if (typeof document === "undefined") return;
  document.documentElement.setAttribute("data-theme", theme);
  document.documentElement.style.colorScheme = theme;
}

export const ThemeProvider: React.FC<{
  children: React.ReactNode;
  initialTheme?: Theme;
}> = ({ children, initialTheme }) => {
  const [theme, setThemeState] = useState<Theme>(() => initialTheme ?? getInitialTheme());

  const setTheme = (newTheme: Theme) => {
    setThemeState(newTheme);
    try {
      localStorage.setItem(STORAGE_KEY, newTheme);
    } catch {
      // Ignored if storage is blocked
    }
    applyThemeToDOM(newTheme);
  };

  const toggleTheme = () => {
    setTheme(theme === "dark" ? "light" : "dark");
  };

  useEffect(() => {
    applyThemeToDOM(theme);
  }, [theme]);

  return (
    <ThemeContext.Provider value={{ theme, setTheme, toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  );
};

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (!context) {
    // Return safe fallback if used outside provider
    return {
      theme: getInitialTheme(),
      setTheme: (t) => applyThemeToDOM(t),
      toggleTheme: () => {
        const current = getInitialTheme();
        applyThemeToDOM(current === "dark" ? "light" : "dark");
      },
    };
  }
  return context;
}
