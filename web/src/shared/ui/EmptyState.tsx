import React from "react";

export interface EmptyStateProps extends React.HTMLAttributes<HTMLDivElement> {
  title: string;
  description?: string;
  icon?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

export const EmptyState: React.FC<EmptyStateProps> = ({
  title,
  description,
  icon,
  action,
  className = "",
  ...props
}) => {
  return (
    <div className={`gw-empty-state ${className}`} {...props}>
      {icon && <div className="gw-empty-icon">{icon}</div>}
      <h4 className="gw-empty-title">{title}</h4>
      {description && <p className="gw-empty-desc">{description}</p>}
      {action && <div className="gw-empty-action">{action}</div>}
    </div>
  );
};
