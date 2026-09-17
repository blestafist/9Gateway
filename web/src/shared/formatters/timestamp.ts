export function formatTimestamp(
  isoString: string | null | undefined,
  locale?: string
): string {
  if (!isoString) {
    return "—";
  }

  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }

  try {
    return new Intl.DateTimeFormat(locale || undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      timeZoneName: "short",
    }).format(date);
  } catch {
    return date.toISOString();
  }
}

export function formatExactTimestamp(isoString: string | null | undefined): string {
  if (!isoString) {
    return "—";
  }
  return isoString;
}
