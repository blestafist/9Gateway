import React from "react";
import { Loader2 } from "lucide-react";

export type IconButtonVariant = "ghost" | "outline" | "secondary" | "primary" | "danger";
export type IconButtonSize = "sm" | "md" | "lg";

export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  "aria-label": string;
  icon: React.ReactNode;
  variant?: IconButtonVariant;
  size?: IconButtonSize;
  isLoading?: boolean;
}

export const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(
  (
    {
      "aria-label": ariaLabel,
      icon,
      variant = "ghost",
      size = "md",
      isLoading = false,
      disabled,
      className = "",
      type = "button",
      ...props
    },
    ref
  ) => {
    const classNames = [
      "gw-icon-btn",
      `gw-icon-btn--${variant}`,
      `gw-icon-btn--${size}`,
      className,
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <button
        ref={ref}
        type={type}
        aria-label={ariaLabel}
        className={classNames}
        disabled={disabled || isLoading}
        aria-busy={isLoading ? true : undefined}
        {...props}
      >
        {isLoading ? (
          <Loader2 className="gw-spinner" size={size === "sm" ? 14 : size === "lg" ? 20 : 16} aria-hidden="true" />
        ) : (
          <span aria-hidden="true" style={{ display: "inline-flex" }}>{icon}</span>
        )}
      </button>
    );
  }
);

IconButton.displayName = "IconButton";
