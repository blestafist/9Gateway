import { Component, ErrorInfo, ReactNode } from "react";
import { Alert, Button } from "../../shared/ui";
import { RotateCcw, Home } from "lucide-react";

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class RouteErrorBoundary extends Component<Props, State> {
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

  private handleRetry = () => {
    this.setState({ hasError: false, error: null });
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
              onClick={() => {
                this.setState({ hasError: false, error: null });
                window.location.href = "/ui/overview";
              }}
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
