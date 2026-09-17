export function formatTokenCount(
  tokens: number | null | undefined,
  compact = false
): string {
  if (tokens === null || tokens === undefined) {
    return "—";
  }

  if (tokens === 0) {
    return "0";
  }

  if (compact) {
    if (tokens >= 1_000_000) {
      return `${(tokens / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
    }
    if (tokens >= 1_000) {
      return `${(tokens / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
    }
  }

  return tokens.toLocaleString();
}

export function formatExactTokenCount(tokens: number | null | undefined): string {
  if (tokens === null || tokens === undefined) {
    return "—";
  }
  return `${tokens.toLocaleString()} tokens`;
}
