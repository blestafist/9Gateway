import React, { useState, useRef, useId } from "react";

export interface TooltipProps {
  content: React.ReactNode;
  children: React.ReactElement<React.HTMLAttributes<HTMLElement>>;
  side?: "top" | "bottom" | "left" | "right";
  delayMs?: number;
}

export const Tooltip: React.FC<TooltipProps> = ({
  content,
  children,
  side = "top",
  delayMs = 200,
}) => {
  const [isVisible, setIsVisible] = useState(false);
  const timeoutRef = useRef<NodeJS.Timeout | null>(null);
  const tooltipId = useId();

  const show = () => {
    timeoutRef.current = setTimeout(() => {
      setIsVisible(true);
    }, delayMs);
  };

  const hide = () => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
    setIsVisible(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape" && isVisible) {
      e.stopPropagation();
      hide();
    }
  };

  return (
    <div
      className="gw-tooltip-wrapper"
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onKeyDown={handleKeyDown}
    >
      {React.cloneElement(children, {
        "aria-describedby": isVisible ? tooltipId : undefined,
      })}
      {isVisible && (
        <div
          id={tooltipId}
          role="tooltip"
          className={`gw-tooltip-content gw-tooltip--${side} gw-tooltip-content--visible`}
        >
          {content}
        </div>
      )}
    </div>
  );
};
