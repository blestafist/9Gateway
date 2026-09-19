import React, { useId } from "react";

export interface SwitchProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label?: string;
  description?: string;
  disabled?: boolean;
  id?: string;
  "aria-label"?: string;
  "data-testid"?: string;
}

export const Switch: React.FC<SwitchProps> = ({
  checked,
  onChange,
  label,
  description,
  disabled = false,
  id: customId,
  "aria-label": customAriaLabel,
  "data-testid": customDataTestId,
}) => {
  const generatedId = useId();
  const switchId = customId || generatedId;
  const labelId = `${switchId}-label`;

  const handleKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (disabled) return;
    if (e.key === " " || e.key === "Enter") {
      e.preventDefault();
      onChange(!checked);
    }
  };

  return (
    <div className="gw-switch-container">
      {(label || description) && (
        <div id={labelId} style={{ display: "flex", flexDirection: "column" }}>
          {label && <span className="gw-checkbox-label-text">{label}</span>}
          {description && <span className="gw-checkbox-description">{description}</span>}
        </div>
      )}
      <button
        type="button"
        id={switchId}
        role="switch"
        aria-checked={checked}
        aria-labelledby={label ? labelId : undefined}
        aria-label={!label ? customAriaLabel : undefined}
        disabled={disabled}
        className="gw-switch"
        data-testid={customDataTestId}
        onClick={() => !disabled && onChange(!checked)}
        onKeyDown={handleKeyDown}
      >
        <span className="gw-switch-thumb" aria-hidden="true" />
      </button>
    </div>
  );
};
