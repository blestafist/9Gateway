let unauthorizedListener: (() => void) | null = null;
let isTransitioning = false;

export function setUnauthorizedListener(listener: (() => void) | null): void {
  unauthorizedListener = listener;
  isTransitioning = false;
}

export function notifyUnauthorized(): void {
  if (isTransitioning || !unauthorizedListener) {
    return;
  }
  isTransitioning = true;
  try {
    unauthorizedListener();
  } finally {
    // Reset transition lock on the next macrotask
    setTimeout(() => {
      isTransitioning = false;
    }, 0);
  }
}
