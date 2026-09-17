let csrfProvider: (() => string | null) | null = null;

export function setCsrfTokenProvider(provider: (() => string | null) | null): void {
  csrfProvider = provider;
}

export function getCsrfToken(): string | null {
  return csrfProvider ? csrfProvider() : null;
}
