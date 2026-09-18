import React, { useEffect, useRef, useId } from "react";
import { X } from "lucide-react";
import { IconButton } from "./IconButton";

export interface DrawerProps {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
  placement?: "right" | "bottom" | "left";
  restoreFocusTo?: React.RefObject<HTMLElement | null>;
}

const FOCUSABLE_SELECTORS =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export const Drawer: React.FC<DrawerProps> = ({
  isOpen,
  onClose,
  title,
  description,
  children,
  footer,
  placement = "right",
  restoreFocusTo,
}) => {
  const drawerId = useId();
  const titleId = `${drawerId}-title`;
  const descId = `${drawerId}-desc`;

  const drawerRef = useRef<HTMLDivElement>(null);
  const previouslyFocusedElementRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!isOpen) return;

    previouslyFocusedElementRef.current =
      restoreFocusTo?.current ?? (document.activeElement as HTMLElement | null);
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const focusTimer = setTimeout(() => {
      if (drawerRef.current) {
        const focusables = drawerRef.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTORS);
        const first = focusables[0];
        if (first) {
          first.focus();
        } else {
          drawerRef.current.focus();
        }
      }
    }, 0);

    return () => {
      clearTimeout(focusTimer);
      document.body.style.overflow = originalOverflow;
      if (previouslyFocusedElementRef.current && typeof previouslyFocusedElementRef.current.focus === "function") {
        previouslyFocusedElementRef.current.focus();
      }
    };
  }, [isOpen, restoreFocusTo]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      onClose();
      return;
    }

    if (e.key === "Tab") {
      if (!drawerRef.current) return;
      const focusables = Array.from(
        drawerRef.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTORS)
      );

      const firstElement = focusables[0];
      const lastElement = focusables[focusables.length - 1];

      if (!firstElement || !lastElement) {
        e.preventDefault();
        return;
      }

      if (e.shiftKey) {
        if (document.activeElement === firstElement || document.activeElement === drawerRef.current) {
          e.preventDefault();
          lastElement.focus();
        }
      } else {
        if (document.activeElement === lastElement) {
          e.preventDefault();
          firstElement.focus();
        }
      }
    }
  };

  if (!isOpen) return null;

  return (
    <div
      className="gw-drawer-backdrop"
      onClick={onClose}
      data-testid="drawer-backdrop"
    >
      <div
        ref={drawerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={`gw-drawer gw-drawer--${placement}`}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <div className="gw-dialog-header">
          <div className="gw-dialog-title-area">
            <h2 id={titleId} className="gw-dialog-title">
              {title}
            </h2>
            {description && (
              <p id={descId} className="gw-dialog-desc">
                {description}
              </p>
            )}
          </div>
          <IconButton
            icon={<X size={18} />}
            aria-label="Close drawer"
            variant="ghost"
            size="sm"
            onClick={onClose}
          />
        </div>

        <div className="gw-dialog-body">{children}</div>

        {footer && <div className="gw-dialog-footer">{footer}</div>}
      </div>
    </div>
  );
};
