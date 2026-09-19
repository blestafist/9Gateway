import React from "react";
import { ChevronDown, ChevronUp } from "lucide-react";

export interface TableProps extends React.TableHTMLAttributes<HTMLTableElement> {
  containerClassName?: string;
  containerAriaLabel?: string;
  children: React.ReactNode;
}

export const Table = React.forwardRef<HTMLTableElement, TableProps>(
  ({ containerClassName = "", containerAriaLabel, className = "", children, ...props }, ref) => {
    const label = containerAriaLabel || props["aria-label"] || "Data table";
    return (
      <div
        className={`gw-table-container ${containerClassName}`}
        role="region"
        aria-label={`${label} scroll region`}
        tabIndex={0}
      >
        <table ref={ref} className={`gw-table ${className}`} {...props}>
          {children}
        </table>
      </div>
    );
  }
);
Table.displayName = "Table";

export const TableHeader = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ children, ...props }, ref) => (
  <thead ref={ref} {...props}>
    {children}
  </thead>
));
TableHeader.displayName = "TableHeader";

export const TableBody = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ children, ...props }, ref) => (
  <tbody ref={ref} {...props}>
    {children}
  </tbody>
));
TableBody.displayName = "TableBody";

export const TableFooter = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ children, ...props }, ref) => (
  <tfoot ref={ref} {...props}>
    {children}
  </tfoot>
));
TableFooter.displayName = "TableFooter";

export const TableRow = React.forwardRef<
  HTMLTableRowElement,
  React.HTMLAttributes<HTMLTableRowElement>
>(({ className = "", children, ...props }, ref) => (
  <tr ref={ref} className={`gw-table-row ${className}`} {...props}>
    {children}
  </tr>
));
TableRow.displayName = "TableRow";

export interface TableHeadProps extends React.ThHTMLAttributes<HTMLTableCellElement> {
  sortDirection?: "asc" | "desc" | null;
  onSort?: () => void;
}

export const TableHead = React.forwardRef<HTMLTableCellElement, TableHeadProps>(
  ({ sortDirection, onSort, children, className = "", ...props }, ref) => {
    const isSortable = Boolean(onSort);
    const classes = [
      className,
      isSortable ? "gw-table-th--sortable" : "",
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <th
        ref={ref}
        className={classes}
        onClick={onSort}
        aria-sort={
          sortDirection === "asc"
            ? "ascending"
            : sortDirection === "desc"
            ? "descending"
            : undefined
        }
        tabIndex={isSortable ? 0 : undefined}
        onKeyDown={
          isSortable
            ? (e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onSort?.();
                }
              }
            : undefined
        }
        {...props}
      >
        <div style={{ display: "inline-flex", alignItems: "center", gap: "0.25rem" }}>
          <span>{children}</span>
          {sortDirection === "asc" && <ChevronUp size={14} aria-hidden="true" />}
          {sortDirection === "desc" && <ChevronDown size={14} aria-hidden="true" />}
        </div>
      </th>
    );
  }
);
TableHead.displayName = "TableHead";

export const TableCell = React.forwardRef<
  HTMLTableCellElement,
  React.TdHTMLAttributes<HTMLTableCellElement>
>(({ children, className = "", ...props }, ref) => (
  <td ref={ref} className={className} {...props}>
    {children}
  </td>
));
TableCell.displayName = "TableCell";
