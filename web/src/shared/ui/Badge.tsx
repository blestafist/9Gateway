import React from "react";

export type BadgeVariant = "default" | "success" | "warning" | "danger" | "info" | "neutral";
export type BadgeSize = "sm" | "md";

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
  size?: BadgeSize;
  dot?: boolean;
  children: React.ReactNode;
}

export const Badge: React.FC<BadgeProps> = ({
  variant = "default",
  size = "md",
  dot = false,
  children,
  className = "",
  ...props
}) => {
  const classNames = [
    "gw-badge",
    `gw-badge--${variant}`,
    `gw-badge--${size}`,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <span className={classNames} {...props}>
      {dot && <span className="gw-badge-dot" aria-hidden="true" />}
      <span>{children}</span>
    </span>
  );
};
