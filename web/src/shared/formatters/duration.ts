export function formatDurationMicros(micros: number | null | undefined): string {
  if (micros === null || micros === undefined) {
    return "—";
  }

  if (micros === 0) {
    return "0 µs";
  }

  if (micros < 1000) {
    return `${micros} µs`;
  }

  if (micros < 1_000_000) {
    const ms = (micros / 1000).toFixed(1).replace(/\.0$/, "");
    return `${ms} ms`;
  }

  const s = parseFloat((micros / 1_000_000).toFixed(2));
  return `${s} s`;
}

export function formatExactDurationMicros(micros: number | null | undefined): string {
  if (micros === null || micros === undefined) {
    return "—";
  }
  return `${micros.toLocaleString()} µs`;
}

export function formatDurationSeconds(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) {
    return "—";
  }

  if (seconds === 0) {
    return "0s";
  }

  if (seconds % 86400 === 0) {
    return `${seconds / 86400}d`;
  }

  if (seconds % 3600 === 0) {
    return `${seconds / 3600}h`;
  }

  if (seconds % 60 === 0) {
    return `${seconds / 60}m`;
  }

  return `${seconds}s`;
}
