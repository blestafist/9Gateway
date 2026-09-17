import React, { useId } from "react";
import { Check, AlertCircle } from "lucide-react";

export interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  label?: React.ReactNode;
  description?: string;
  error?: string;
}

export const Checkbox = React.forwardRef<HTMLInputElement, CheckboxProps>(
  (
    {
      label,
      description,
      error,
      checked,
      disabled,
      className = "",
      id: customId,
      ...props
    },
    ref
  ) => {
    const generatedId = useId();
    const checkboxId = customId || generatedId;
    const errorId = `${checkboxId}-error`;

    return (
      <div className="gw-form-control">
        <label
          htmlFor={checkboxId}
          className={`gw-checkbox-wrapper ${disabled ? "gw-checkbox-wrapper--disabled" : ""} ${className}`}
        >
          <input
            ref={ref}
            type="checkbox"
            id={checkboxId}
            className="gw-checkbox-input"
            checked={checked}
            disabled={disabled}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? errorId : undefined}
            {...props}
          />
          <div className="gw-checkbox-box" aria-hidden="true">
            {checked && <Check size={12} strokeWidth={3} />}
          </div>
          {(label || description) && (
            <div style={{ display: "flex", flexDirection: "column" }}>
              {label && <span className="gw-checkbox-label-text">{label}</span>}
              {description && <span className="gw-checkbox-description">{description}</span>}
            </div>
          )}
        </label>
        {error && (
          <div id={errorId} className="gw-form-error" role="alert">
            <AlertCircle size={14} aria-hidden="true" />
            <span>{error}</span>
          </div>
        )}
      </div>
    );
  }
);

Checkbox.displayName = "Checkbox";
