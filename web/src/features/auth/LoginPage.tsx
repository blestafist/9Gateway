import React, { useState } from "react";
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

export const LoginPage: React.FC = () => {
  const [adminToken, setAdminToken] = useState("");
  const [showPassword, setShowPassword] = useState(false);

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

        <CardContent>
          <div className="gw-form-stack">
            <Input
              type={showPassword ? "text" : "password"}
              label="Admin Credential"
              placeholder="gw_admin_••••••••"
              value={adminToken}
              onChange={(e) => setAdminToken(e.target.value)}
              leftIcon={<Lock size={16} />}
              rightIcon={
                <button
                  type="button"
                  aria-label={showPassword ? "Hide credential" : "Show credential"}
                  onClick={() => setShowPassword(!showPassword)}
                  className="gw-input-reveal-btn"
                >
                  {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              }
              helperText="Admin credential configured via GATEWAY_ADMIN_KEY."
            />

            <Alert variant="info" title="Authentication Placeholder (T163)">
              Secure browser sessions, HttpOnly cookies, and CSRF protection will connect in T164.
            </Alert>
          </div>
        </CardContent>

        <CardFooter>
          <Button
            variant="primary"
            style={{ width: "100%" }}
            disabled
            title="Session authentication connects in T164"
          >
            Sign In
          </Button>
        </CardFooter>
      </Card>
    </div>
  );
};

export default LoginPage;
