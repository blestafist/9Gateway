import React from "react";
import { Info, AlertTriangle, AlertCircle, CheckCircle2, X } from "lucide-react";
import { IconButton } from "./IconButton";

export type AlertVariant = "info" | "warning" | "danger" | "success";

export interface AlertProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: AlertVariant;
  title?: string;
  icon?: React.ReactNode;
  onClose?: () => void;
  children?: React.ReactNode;
}

export const Alert: React.FC<AlertProps> = ({
  variant = "info",
  title,
  icon,
  onClose,
  children,
  className = "",
  ...props
}) => {
  const getDefaultIcon = () => {
    switch (variant) {
      case "danger":
        return <AlertCircle size={18} aria-hidden="true" />;
      case "warning":
        return <AlertTriangle size={18} aria-hidden="true" />;
      case "success":
        return <CheckCircle2 size={18} aria-hidden="true" />;
      case "info":
      default:
        return <Info size={18} aria-hidden="true" />;
    }
  };

  const isAssertive = variant === "danger" || variant === "warning";
  const role = isAssertive ? "alert" : "status";

  return (
    <div
      role={role}
      className={`gw-alert gw-alert--${variant} ${className}`}
      {...props}
    >
      <div className="gw-alert-icon">{icon || getDefaultIcon()}</div>
      <div className="gw-alert-content">
        {title && <div className="gw-alert-title">{title}</div>}
        {children && <div>{children}</div>}
      </div>
      {onClose && (
        <IconButton
          icon={<X size={16} />}
          aria-label="Dismiss alert"
          variant="ghost"
          size="sm"
          onClick={onClose}
        />
      )}
    </div>
  );
};
