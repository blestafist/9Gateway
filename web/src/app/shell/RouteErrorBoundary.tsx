import React, { Component, ErrorInfo, ReactNode } from "react";
import { useInRouterContext, useNavigate, useLocation } from "react-router-dom";
import { Alert, Button } from "../../shared/ui";
import { RotateCcw, Home } from "lucide-react";

interface InnerProps {
  children: ReactNode;
  locationKey?: string;
  onNavigateHome?: () => void;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

class RouteErrorBoundaryInner extends Component<InnerProps, State> {
  public state: State = {
    hasError: false,
    error: null,
  };

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    // Log safe error trace in dev mode
    if (import.meta.env.DEV) {
      console.error("RouteErrorBoundary caught error:", error, errorInfo);
    }
  }

  public componentDidUpdate(prevProps: InnerProps) {
    // Automatically recover without full reload when user navigates away safely
    if (prevProps.locationKey !== this.props.locationKey && this.state.hasError) {
      this.setState({ hasError: false, error: null });
    }
  }

  private handleRetry = () => {
    this.setState({ hasError: false, error: null });
  };

  private handleReturnHome = () => {
    this.setState({ hasError: false, error: null });
    if (this.props.onNavigateHome) {
      this.props.onNavigateHome();
    } else if (typeof window !== "undefined") {
      window.location.href = "/ui/overview";
    }
  };

  public render() {
    if (this.state.hasError) {
      return (
        <div className="gw-route-error-screen" data-testid="route-error-boundary">
          <Alert variant="danger" title="Failed to Load Route">
            {this.state.error?.message ||
              "An unexpected error occurred while loading this page chunk."}
          </Alert>
          <div className="gw-route-error-actions">
            <Button
              variant="primary"
              size="sm"
              leftIcon={<RotateCcw size={16} />}
              onClick={this.handleRetry}
            >
              Try Again
            </Button>
            <Button
              variant="secondary"
              size="sm"
              leftIcon={<Home size={16} />}
              onClick={this.handleReturnHome}
            >
              Return to Overview
            </Button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}

const RouteErrorBoundaryRouterBridge: React.FC<{ children: ReactNode }> = ({ children }) => {
  const navigate = useNavigate();
  const location = useLocation();
  return (
    <RouteErrorBoundaryInner
      locationKey={location.key}
      onNavigateHome={() => navigate("/overview")}
    >
      {children}
    </RouteErrorBoundaryInner>
  );
};

export const RouteErrorBoundary: React.FC<{ children: ReactNode }> = ({ children }) => {
  const inRouter = useInRouterContext();
  if (inRouter) {
    return <RouteErrorBoundaryRouterBridge>{children}</RouteErrorBoundaryRouterBridge>;
  }
  return <RouteErrorBoundaryInner>{children}</RouteErrorBoundaryInner>;
};
