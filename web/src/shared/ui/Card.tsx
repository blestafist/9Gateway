import React from "react";

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  hoverable?: boolean;
  children: React.ReactNode;
}

export const Card = React.forwardRef<HTMLDivElement, CardProps>(
  ({ hoverable = false, children, className = "", ...props }, ref) => {
    const classNames = [
      "gw-card",
      hoverable ? "gw-card--hoverable" : "",
      className,
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <div ref={ref} className={classNames} {...props}>
        {children}
      </div>
    );
  }
);
Card.displayName = "Card";

export const CardHeader = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ children, className = "", ...props }, ref) => (
  <div ref={ref} className={`gw-card-header ${className}`} {...props}>
    {children}
  </div>
));
CardHeader.displayName = "CardHeader";

export const CardTitle = React.forwardRef<
  HTMLHeadingElement,
  React.HTMLAttributes<HTMLHeadingElement>
>(({ children, className = "", ...props }, ref) => (
  <h3 ref={ref} className={`gw-card-title ${className}`} {...props}>
    {children}
  </h3>
));
CardTitle.displayName = "CardTitle";

export const CardDescription = React.forwardRef<
  HTMLParagraphElement,
  React.HTMLAttributes<HTMLParagraphElement>
>(({ children, className = "", ...props }, ref) => (
  <p ref={ref} className={`gw-card-description ${className}`} {...props}>
    {children}
  </p>
));
CardDescription.displayName = "CardDescription";

export const CardContent = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ children, className = "", ...props }, ref) => (
  <div ref={ref} className={`gw-card-content ${className}`} {...props}>
    {children}
  </div>
));
CardContent.displayName = "CardContent";

export const CardFooter = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ children, className = "", ...props }, ref) => (
  <div ref={ref} className={`gw-card-footer ${className}`} {...props}>
    {children}
  </div>
));
CardFooter.displayName = "CardFooter";
