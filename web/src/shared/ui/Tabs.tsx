import React, { useId, useRef } from "react";

export interface TabItem {
  id: string;
  label: React.ReactNode;
  disabled?: boolean;
  content?: React.ReactNode;
}

export interface TabsProps {
  items: TabItem[];
  activeTab: string;
  onChange: (tabId: string) => void;
  variant?: "segmented" | "line";
  "aria-label"?: string;
  className?: string;
}

export const Tabs: React.FC<TabsProps> = ({
  items,
  activeTab,
  onChange,
  variant = "segmented",
  "aria-label": ariaLabel,
  className = "",
}) => {
  const baseId = useId();
  const tabRefs = useRef<Map<string, HTMLButtonElement>>(new Map());

  const enabledItems = items.filter((item) => !item.disabled);

  const handleKeyDown = (e: React.KeyboardEvent, currentId: string) => {
    const currentIndex = enabledItems.findIndex((item) => item.id === currentId);
    if (currentIndex === -1) return;

    let targetIndex = -1;

    if (e.key === "ArrowRight") {
      e.preventDefault();
      targetIndex = (currentIndex + 1) % enabledItems.length;
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      targetIndex = (currentIndex - 1 + enabledItems.length) % enabledItems.length;
    } else if (e.key === "Home") {
      e.preventDefault();
      targetIndex = 0;
    } else if (e.key === "End") {
      e.preventDefault();
      targetIndex = enabledItems.length - 1;
    }

    if (targetIndex !== -1) {
      const targetItem = enabledItems[targetIndex];
      if (targetItem) {
        onChange(targetItem.id);
        tabRefs.current.get(targetItem.id)?.focus();
      }
    }
  };

  const activeItem = items.find((item) => item.id === activeTab);

  return (
    <div className={`gw-tabs-container ${className}`}>
      <div
        role="tablist"
        aria-label={ariaLabel}
        className={`gw-tablist gw-tablist--${variant}`}
      >
        {items.map((item) => {
          const isSelected = item.id === activeTab;
          const tabId = `${baseId}-tab-${item.id}`;
          const panelId = `${baseId}-panel-${item.id}`;

          return (
            <button
              key={item.id}
              ref={(el) => {
                if (el) tabRefs.current.set(item.id, el);
                else tabRefs.current.delete(item.id);
              }}
              id={tabId}
              role="tab"
              type="button"
              aria-selected={isSelected}
              aria-controls={panelId}
              tabIndex={isSelected ? 0 : -1}
              disabled={item.disabled}
              className="gw-tab"
              onClick={() => !item.disabled && onChange(item.id)}
              onKeyDown={(e) => handleKeyDown(e, item.id)}
            >
              {item.label}
            </button>
          );
        })}
      </div>

      {activeItem && activeItem.content && (
        <div
          id={`${baseId}-panel-${activeItem.id}`}
          role="tabpanel"
          aria-labelledby={`${baseId}-tab-${activeItem.id}`}
          tabIndex={0}
          className="gw-tabpanel"
        >
          {activeItem.content}
        </div>
      )}
    </div>
  );
};
