export function formatNullable<T>(
  value: T | null | undefined,
  formatter?: (val: T) => string,
  fallback = "—"
): string {
  if (value === null || value === undefined) {
    return fallback;
  }

  if (typeof value === "string" && value.trim() === "") {
    return fallback;
  }

  if (formatter) {
    return formatter(value);
  }

  return String(value);
}
