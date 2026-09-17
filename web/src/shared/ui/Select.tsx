import React, { useId } from "react";
import { ChevronDown, AlertCircle } from "lucide-react";

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  label?: string;
  error?: string;
  helperText?: string;
  options?: SelectOption[];
}

export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  (
    {
      label,
      error,
      helperText,
      options,
      children,
      id: customId,
      className = "",
      disabled,
      ...props
    },
    ref
  ) => {
    const generatedId = useId();
    const selectId = customId || generatedId;
    const errorId = `${selectId}-error`;
    const helperId = `${selectId}-helper`;

    const describedBy = [
      error ? errorId : null,
      helperText && !error ? helperId : null,
    ]
      .filter(Boolean)
      .join(" ");

    const selectClasses = [
      "gw-select",
      error ? "gw-input--error" : "",
      className,
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <div className="gw-form-control">
        {label && (
          <label htmlFor={selectId} className="gw-form-label">
            {label}
          </label>
        )}
        <div className="gw-select-wrapper">
          <select
            ref={ref}
            id={selectId}
            className={selectClasses}
            disabled={disabled}
            aria-invalid={error ? true : undefined}
            aria-describedby={describedBy || undefined}
            {...props}
          >
            {options
              ? options.map((opt) => (
                  <option key={opt.value} value={opt.value} disabled={opt.disabled}>
                    {opt.label}
                  </option>
                ))
              : children}
          </select>
          <ChevronDown className="gw-select-chevron" size={16} aria-hidden="true" />
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

Select.displayName = "Select";
