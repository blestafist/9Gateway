import React, { useState, useRef, useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  CardFooter,
  Button,
  Input,
  Alert,
} from "../../shared/ui";
import { Lock, ShieldCheck, Eye, EyeOff } from "lucide-react";
import { useAuth } from "./AuthContext";

export const LoginPage: React.FC = () => {
  const [credential, setCredential] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const errorRef = useRef<HTMLDivElement>(null);
  const { isAuthenticated, login } = useAuth();
  const location = useLocation();
  const navigate = useNavigate();

  // Extract originally requested route if redirected from a protected route
  const fromState = (location.state as { from?: { pathname: string; search?: string } })?.from;
  const returnTo = fromState ? `${fromState.pathname}${fromState.search || ""}` : "/overview";

  useEffect(() => {
    if (isAuthenticated) {
      navigate(returnTo, { replace: true });
    }
  }, [isAuthenticated, navigate, returnTo]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const secret = credential.trim();
    if (!secret || isSubmitting) return;

    // Clear credential in local component state immediately
    setCredential("");
    setError(null);
    setIsSubmitting(true);

    try {
      await login(secret);
      navigate(returnTo, { replace: true });
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : "Authentication failed";
      setError(msg);
      // Focus the error alert for screen reader announcements and keyboard users
      setTimeout(() => {
        errorRef.current?.focus();
      }, 50);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="gw-login-container" data-testid="login-page">
      <Card className="gw-login-card">
        <CardHeader>
          <div className="gw-login-header">
            <div className="gw-login-badge">
              <ShieldCheck size={28} style={{ color: "var(--accent-primary)" }} aria-hidden="true" />
            </div>
            <CardTitle>Console Sign In</CardTitle>
            <p className="gw-card-subtitle">
              Authenticate with your 9Gateway admin credential to access operations.
            </p>
          </div>
        </CardHeader>

        <form onSubmit={handleSubmit} method="post" action="#" noValidate>
          <CardContent>
            <div className="gw-form-stack">
              {/* Hidden username input for password manager semantics */}
              <input
                type="text"
                name="username"
                defaultValue="admin"
                autoComplete="username"
                style={{ display: "none" }}
                tabIndex={-1}
                readOnly
                aria-hidden="true"
              />

              {error && (
                <div
                  ref={errorRef}
                  tabIndex={-1}
                  style={{ outline: "none" }}
                  data-testid="login-error-container"
                >
                  <Alert
                    variant="danger"
                    title="Authentication Error"
                    aria-live="assertive"
                    data-testid="login-alert"
                  >
                    {error}
                  </Alert>
                </div>
              )}

              <Input
                id="admin-credential"
                name="password"
                type={showPassword ? "text" : "password"}
                label="Admin Credential"
                placeholder="gw_admin_••••••••"
                value={credential}
                onChange={(e) => {
                  setCredential(e.target.value);
                  if (error) setError(null);
                }}
                autoComplete="current-password"
                spellCheck={false}
                autoCapitalize="none"
                autoCorrect="off"
                required
                disabled={isSubmitting}
                leftIcon={<Lock size={16} />}
                actionIcon={showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                actionLabel={showPassword ? "Hide credential" : "Show credential"}
                onActionClick={() => setShowPassword(!showPassword)}
                helperText="Admin credential configured via GATEWAY_ADMIN_KEY."
              />
            </div>
          </CardContent>

          <CardFooter>
            <Button
              type="submit"
              variant="primary"
              style={{ width: "100%" }}
              disabled={isSubmitting || !credential.trim()}
              isLoading={isSubmitting}
              data-testid="login-submit-btn"
            >
              {isSubmitting ? "Signing in..." : "Sign In"}
            </Button>
          </CardFooter>
        </form>
      </Card>
    </div>
  );
};

export default LoginPage;
