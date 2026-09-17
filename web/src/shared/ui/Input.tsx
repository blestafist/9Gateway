import React, { useId } from "react";
import { AlertCircle } from "lucide-react";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  helperText?: string;
  leftIcon?: React.ReactNode;
  rightIcon?: React.ReactNode;
  actionIcon?: React.ReactNode;
  onActionClick?: () => void;
  actionLabel?: string;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  (
    {
      label,
      error,
      helperText,
      leftIcon,
      rightIcon,
      actionIcon,
      onActionClick,
      actionLabel,
      id: customId,
      className = "",
      disabled,
      ...props
    },
    ref
  ) => {
    const generatedId = useId();
    const inputId = customId || generatedId;
    const errorId = `${inputId}-error`;
    const helperId = `${inputId}-helper`;

    const describedBy = [
      error ? errorId : null,
      helperText && !error ? helperId : null,
    ]
      .filter(Boolean)
      .join(" ");

    const inputClasses = [
      "gw-input",
      leftIcon ? "gw-input--has-left-icon" : "",
      rightIcon || actionIcon ? "gw-input--has-right-icon" : "",
      error ? "gw-input--error" : "",
      className,
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <div className="gw-form-control">
        {label && (
          <label htmlFor={inputId} className="gw-form-label">
            {label}
          </label>
        )}
        <div className="gw-input-wrapper">
          {leftIcon && <span className="gw-input-icon-left" aria-hidden="true">{leftIcon}</span>}
          <input
            ref={ref}
            id={inputId}
            className={inputClasses}
            disabled={disabled}
            aria-invalid={error ? true : undefined}
            aria-describedby={describedBy || undefined}
            {...props}
          />
          {actionIcon ? (
            <button
              type="button"
              className="gw-input-icon-right gw-input-action"
              onClick={onActionClick}
              aria-label={actionLabel || "Action"}
              tabIndex={0}
              style={{ background: "none", border: "none", padding: 0 }}
            >
              {actionIcon}
            </button>
          ) : rightIcon ? (
            <span className="gw-input-icon-right" aria-hidden="true">{rightIcon}</span>
          ) : null}
        </div>
        {error ? (
          <div id={errorId} className="gw-form-error" role="alert">
            <AlertCircle size={14} aria-hidden="true" />
            <span>{error}</span>
          </div>
        ) : helperText ? (
          <div id={helperId} className="gw-form-helper">
            {helperText}
          </div>
        ) : null}
      </div>
    );
  }
);

Input.displayName = "Input";
