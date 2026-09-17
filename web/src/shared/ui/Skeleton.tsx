import React from "react";

export type SkeletonVariant = "text" | "rect" | "circle";

export interface SkeletonProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: SkeletonVariant;
  width?: string | number;
  height?: string | number;
}

export const Skeleton: React.FC<SkeletonProps> = ({
  variant = "text",
  width,
  height,
  className = "",
  style,
  ...props
}) => {
  const classes = [
    "gw-skeleton",
    `gw-skeleton--${variant}`,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  const inlineStyle: React.CSSProperties = {
    width,
    height,
    ...style,
  };

  return <div className={classes} style={inlineStyle} aria-hidden="true" {...props} />;
};
