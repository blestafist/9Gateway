import React, { createContext, useContext, useState, useCallback, useRef } from "react";
import { Info, AlertTriangle, AlertCircle, CheckCircle2, X } from "lucide-react";
import { IconButton } from "./IconButton";

export type ToastVariant = "info" | "success" | "warning" | "danger";

export interface ToastItem {
  id: string;
  title: string;
  description?: string;
  variant?: ToastVariant;
  duration?: number;
}

interface ToastContextValue {
  show: (toast: Omit<ToastItem, "id"> & { id?: string }) => string;
  dismiss: (id: string) => void;
}

const ToastContext = createContext<ToastContextValue | undefined>(undefined);

export const useToast = (): ToastContextValue => {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error("useToast must be used within a ToastProvider");
  }
  return ctx;
};

const ToastMessage: React.FC<{
  item: ToastItem;
  onDismiss: (id: string) => void;
}> = ({ item, onDismiss }) => {
  const timerRef = useRef<NodeJS.Timeout | null>(null);
  const remainingRef = useRef<number>(item.duration ?? 5000);
  const startRef = useRef<number>(Date.now());

  const startTimer = useCallback(() => {
    if (remainingRef.current <= 0) return;
    startRef.current = Date.now();
    timerRef.current = setTimeout(() => {
      onDismiss(item.id);
    }, remainingRef.current);
  }, [item.id, onDismiss]);

  const pauseTimer = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
      remainingRef.current -= Date.now() - startRef.current;
    }
  }, []);

  React.useEffect(() => {
    startTimer();
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [startTimer]);

  const variant = item.variant || "info";
  const isAssertive = variant === "danger";

  const getIcon = () => {
    switch (variant) {
      case "danger":
        return <AlertCircle size={18} color="var(--status-danger)" aria-hidden="true" />;
      case "warning":
        return <AlertTriangle size={18} color="var(--status-warning)" aria-hidden="true" />;
      case "success":
        return <CheckCircle2 size={18} color="var(--status-success)" aria-hidden="true" />;
      case "info":
      default:
        return <Info size={18} color="var(--status-info)" aria-hidden="true" />;
    }
  };

  return (
    <div
      role={isAssertive ? "alert" : "status"}
      aria-live={isAssertive ? "assertive" : "polite"}
      aria-atomic="true"
      className={`gw-toast gw-toast--${variant}`}
      onMouseEnter={pauseTimer}
      onMouseLeave={startTimer}
      onFocus={pauseTimer}
      onBlur={startTimer}
      data-testid="toast-item"
    >
      <div style={{ display: "flex", flexShrink: 0, marginTop: 2 }}>{getIcon()}</div>
      <div className="gw-toast-body">
        <div className="gw-toast-title">{item.title}</div>
        {item.description && <div className="gw-toast-desc">{item.description}</div>}
      </div>
      <IconButton
        icon={<X size={16} />}
        aria-label="Close notification"
        variant="ghost"
        size="sm"
        onClick={() => onDismiss(item.id)}
      />
    </div>
  );
};

export const ToastProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const counterRef = useRef(0);

  const dismiss = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const show = useCallback(
    (toast: Omit<ToastItem, "id"> & { id?: string }): string => {
      const id = toast.id || `toast-${Date.now()}-${++counterRef.current}`;
      const newItem: ToastItem = { ...toast, id };
      setToasts((prev) => [...prev, newItem]);
      return id;
    },
    []
  );

  return (
    <ToastContext.Provider value={{ show, dismiss }}>
      {children}
      <div className="gw-toast-region" aria-label="Notifications" role="region">
        {toasts.map((toast) => (
          <ToastMessage key={toast.id} item={toast} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext.Provider>
  );
};
