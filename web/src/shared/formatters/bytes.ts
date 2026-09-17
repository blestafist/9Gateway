export function formatByteCount(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) {
    return "—";
  }

  if (bytes === 0) {
    return "0 B";
  }

  if (bytes < 1024) {
    return `${bytes} B`;
  }

  if (bytes < 1024 * 1024) {
    const kb = (bytes / 1024).toFixed(1).replace(/\.0$/, "");
    return `${kb} KB`;
  }

  if (bytes < 1024 * 1024 * 1024) {
    const mb = (bytes / (1024 * 1024)).toFixed(1).replace(/\.0$/, "");
    return `${mb} MB`;
  }

  const gb = parseFloat((bytes / (1024 * 1024 * 1024)).toFixed(2));
  return `${gb} GB`;
}

export function formatExactByteCount(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) {
    return "—";
  }
  return `${bytes.toLocaleString()} bytes`;
}
