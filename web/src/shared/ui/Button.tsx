import React from "react";
import { Loader2 } from "lucide-react";

export type ButtonVariant = "primary" | "secondary" | "outline" | "ghost" | "danger";
export type ButtonSize = "sm" | "md" | "lg";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  isLoading?: boolean;
  leftIcon?: React.ReactNode;
  rightIcon?: React.ReactNode;
  children?: React.ReactNode;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      variant = "secondary",
      size = "md",
      isLoading = false,
      leftIcon,
      rightIcon,
      children,
      disabled,
      className = "",
      type = "button",
      ...props
    },
    ref
  ) => {
    const classNames = [
      "gw-btn",
      `gw-btn--${variant}`,
      `gw-btn--${size}`,
      className,
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <button
        ref={ref}
        type={type}
        className={classNames}
        disabled={disabled || isLoading}
        aria-busy={isLoading ? true : undefined}
        {...props}
      >
        {isLoading ? (
          <Loader2 className="gw-spinner" size={size === "sm" ? 14 : size === "lg" ? 20 : 16} aria-hidden="true" />
        ) : (
          leftIcon && <span aria-hidden="true" style={{ display: "inline-flex" }}>{leftIcon}</span>
        )}
        {children && <span>{children}</span>}
        {!isLoading && rightIcon && (
          <span aria-hidden="true" style={{ display: "inline-flex" }}>{rightIcon}</span>
        )}
      </button>
    );
  }
);

Button.displayName = "Button";
