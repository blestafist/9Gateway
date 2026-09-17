import React, { useEffect, useRef, useId } from "react";
import { X } from "lucide-react";
import { IconButton } from "./IconButton";

export interface DialogProps {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
  size?: "sm" | "md" | "lg";
  closeOnBackdropClick?: boolean;
}

const FOCUSABLE_SELECTORS =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export const Dialog: React.FC<DialogProps> = ({
  isOpen,
  onClose,
  title,
  description,
  children,
  footer,
  size = "md",
  closeOnBackdropClick = true,
}) => {
  const dialogId = useId();
  const titleId = `${dialogId}-title`;
  const descId = `${dialogId}-desc`;

  const dialogRef = useRef<HTMLDivElement>(null);
  const previouslyFocusedElementRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!isOpen) return;

    previouslyFocusedElementRef.current = document.activeElement as HTMLElement | null;
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    // Set initial focus inside dialog
    const focusTimer = setTimeout(() => {
      if (dialogRef.current) {
        const focusables = dialogRef.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTORS);
        const first = focusables[0];
        if (first) {
          first.focus();
        } else {
          dialogRef.current.focus();
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
  }, [isOpen]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      onClose();
      return;
    }

    if (e.key === "Tab") {
      if (!dialogRef.current) return;
      const focusables = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTORS)
      ).filter((el) => el.offsetParent !== null || el === document.activeElement);

      const firstElement = focusables[0];
      const lastElement = focusables[focusables.length - 1];

      if (!firstElement || !lastElement) {
        e.preventDefault();
        return;
      }

      if (e.shiftKey) {
        if (document.activeElement === firstElement || document.activeElement === dialogRef.current) {
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
      className="gw-dialog-backdrop"
      onClick={closeOnBackdropClick ? onClose : undefined}
      data-testid="dialog-backdrop"
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={`gw-dialog gw-dialog--${size}`}
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
            aria-label="Close dialog"
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
