import React, { createContext, useContext, useEffect, useState, useCallback, useRef } from "react";
import { setCsrfTokenProvider, setUnauthorizedListener } from "../../shared/transport";
import { clearAdminCache } from "../../shared/query";

export interface SessionState {
  authenticated: boolean;
  csrf_token?: string;
  idle_expires_at?: string;
  expires_at?: string;
}

export interface AuthContextValue {
  isAuthenticated: boolean;
  isLoading: boolean;
  csrfToken: string | null;
  idleExpiresAt: string | null;
  expiresAt: string | null;
  login: (credential: string) => Promise<void>;
  logout: () => Promise<void>;
  checkSession: () => Promise<boolean>;
  expireSession: () => void;
}

export interface AuthProviderProps {
  children: React.ReactNode;
  initialAuthState?: {
    isAuthenticated: boolean;
    csrfToken?: string;
    idleExpiresAt?: string;
    expiresAt?: string;
  };
}

export const AuthContext = createContext<AuthContextValue | null>(null);

export const AuthProvider: React.FC<AuthProviderProps> = ({
  children,
  initialAuthState,
}) => {
  const [isAuthenticated, setIsAuthenticated] = useState<boolean>(
    initialAuthState ? initialAuthState.isAuthenticated : false
  );
  const [isLoading, setIsLoading] = useState<boolean>(!initialAuthState);
  const [csrfToken, setCsrfToken] = useState<string | null>(
    initialAuthState?.csrfToken || null
  );
  const [idleExpiresAt, setIdleExpiresAt] = useState<string | null>(
    initialAuthState?.idleExpiresAt || null
  );
  const [expiresAt, setExpiresAt] = useState<string | null>(
    initialAuthState?.expiresAt || null
  );

  const csrfTokenRef = useRef(csrfToken);
  useEffect(() => {
    csrfTokenRef.current = csrfToken;
  }, [csrfToken]);

  useEffect(() => {
    setCsrfTokenProvider(() => csrfTokenRef.current);
    return () => {
      setCsrfTokenProvider(null);
    };
  }, []);

  const expireSession = useCallback(() => {
    setIsAuthenticated(false);
    setCsrfToken(null);
    setIdleExpiresAt(null);
    setExpiresAt(null);
    clearAdminCache();
  }, []);

  useEffect(() => {
    setUnauthorizedListener(() => {
      expireSession();
    });
    return () => {
      setUnauthorizedListener(null);
    };
  }, [expireSession]);

  const checkSession = useCallback(async (): Promise<boolean> => {
    try {
      const response = await fetch("/admin/ui/v1/session", {
        method: "GET",
        headers: {
          Accept: "application/json",
        },
        credentials: "same-origin",
      });
      if (response.ok) {
        const data: SessionState = await response.json();
        if (data.authenticated && data.csrf_token) {
          setIsAuthenticated(true);
          setCsrfToken(data.csrf_token);
          setIdleExpiresAt(data.idle_expires_at || null);
          setExpiresAt(data.expires_at || null);
          return true;
        }
      }
      setIsAuthenticated(false);
      setCsrfToken(null);
      setIdleExpiresAt(null);
      setExpiresAt(null);
      return false;
    } catch {
      setIsAuthenticated(false);
      setCsrfToken(null);
      setIdleExpiresAt(null);
      setExpiresAt(null);
      return false;
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!initialAuthState) {
      void checkSession();
    }
  }, [checkSession, initialAuthState]);

  const login = useCallback(async (credential: string) => {
    const response = await fetch("/admin/ui/v1/session", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      credentials: "same-origin",
      body: JSON.stringify({ credential }),
    });

    if (response.ok) {
      const data: SessionState = await response.json();
      setIsAuthenticated(true);
      setCsrfToken(data.csrf_token || null);
      setIdleExpiresAt(data.idle_expires_at || null);
      setExpiresAt(data.expires_at || null);
      return;
    }

    let message = "Invalid admin credential. Please check your credentials.";
    try {
      const errData = await response.json();
      if (response.status === 429) {
        message = "Too many failed login attempts. Please try again later.";
      } else if (errData?.error?.message) {
        message = errData.error.message;
      }
    } catch {
      // Keep generic message
    }
    setIsAuthenticated(false);
    setCsrfToken(null);
    setIdleExpiresAt(null);
    setExpiresAt(null);
    throw new Error(message);
  }, []);

  const logout = useCallback(async () => {
    try {
      await fetch("/admin/ui/v1/session", {
        method: "DELETE",
        headers: {
          ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
        },
        credentials: "same-origin",
      });
    } finally {
      setIsAuthenticated(false);
      setCsrfToken(null);
      setIdleExpiresAt(null);
      setExpiresAt(null);
      clearAdminCache();
    }
  }, [csrfToken]);

  return (
    <AuthContext.Provider
      value={{
        isAuthenticated,
        isLoading,
        csrfToken,
        idleExpiresAt,
        expiresAt,
        login,
        logout,
        checkSession,
        expireSession,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = (): AuthContextValue => {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return context;
};
