export function formatCostMicros(micros: number | null | undefined): string {
  if (micros === null || micros === undefined) {
    return "—";
  }

  if (micros === 0) {
    return "$0.00";
  }

  const dollars = micros / 1_000_000;
  if (dollars < 0.0001) {
    return `$${dollars.toFixed(6)}`;
  }
  if (dollars < 0.01) {
    return `$${dollars.toFixed(4)}`;
  }
  return `$${dollars.toFixed(2)}`;
}

export function formatExactCostMicros(micros: number | null | undefined): string {
  if (micros === null || micros === undefined) {
    return "—";
  }

  if (micros === 0) {
    return "$0.000000 (0 µ$)";
  }

  const dollars = (micros / 1_000_000).toFixed(6);
  return `$${dollars} (${micros.toLocaleString()} µ$)`;
}
