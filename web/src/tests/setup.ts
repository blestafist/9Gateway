import "@testing-library/jest-dom/vitest";

// Ensure a reliable localStorage implementation in jsdom / Node 22+
const store = new Map<string, string>();
const localStorageMock = {
  getItem: (key: string): string | null => store.get(key) ?? null,
  setItem: (key: string, value: string): void => {
    store.set(key, String(value));
  },
  removeItem: (key: string): void => {
    store.delete(key);
  },
  clear: (): void => {
    store.clear();
  },
  get length(): number {
    return store.size;
  },
  key: (index: number): string | null => {
    return Array.from(store.keys())[index] ?? null;
  },
};

Object.defineProperty(window, "localStorage", {
  value: localStorageMock,
  writable: true,
});

Object.defineProperty(globalThis, "localStorage", {
  value: localStorageMock,
  writable: true,
});

// Provide standard ResizeObserver mock for charts in jsdom
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

// Provide standard window.matchMedia mock for media queries / reduced motion in jsdom
if (typeof window !== "undefined" && typeof window.matchMedia === "undefined") {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}

// Provide standard URL.createObjectURL / revokeObjectURL for Blob downloads in jsdom
if (typeof URL.createObjectURL === "undefined") {
  URL.createObjectURL = (blob: Blob) => `blob:mock-url-${blob.size}`;
}
if (typeof URL.revokeObjectURL === "undefined") {
  URL.revokeObjectURL = () => {};
}

// Provide canvas getContext mock to suppress jsdom warnings during axe runs
if (typeof window !== "undefined" && window.HTMLCanvasElement) {
  window.HTMLCanvasElement.prototype.getContext = (() => null) as unknown as typeof HTMLCanvasElement.prototype.getContext;
}

