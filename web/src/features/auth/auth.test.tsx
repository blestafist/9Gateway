import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { LoginPage } from "./LoginPage";
import { AuthContext, AuthContextValue } from "./AuthContext";

describe("LoginPage", () => {
  const createMockAuth = (loginMock = vi.fn().mockResolvedValue(undefined)): AuthContextValue => ({
    isAuthenticated: false,
    isLoading: false,
    csrfToken: null,
    idleExpiresAt: null,
    expiresAt: null,
    login: loginMock,
    logout: vi.fn(),
    checkSession: vi.fn().mockResolvedValue(false),
    expireSession: vi.fn(),
  });

  it("preserves leading and trailing whitespace in credentials when submitting", async () => {
    const loginMock = vi.fn().mockResolvedValue(undefined);
    const mockAuth = createMockAuth(loginMock);

    render(
      <AuthContext.Provider value={mockAuth}>
        <MemoryRouter>
          <LoginPage />
        </MemoryRouter>
      </AuthContext.Provider>
    );

    const input = screen.getByLabelText("Admin Credential");
    const credentialWithSpaces = "  secret_with_spaces  ";
    fireEvent.change(input, { target: { value: credentialWithSpaces } });

    const submitBtn = screen.getByTestId("login-submit-btn");
    expect(submitBtn).not.toBeDisabled();

    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(loginMock).toHaveBeenCalledTimes(1);
    });

    expect(loginMock).toHaveBeenCalledWith("  secret_with_spaces  ");
  });

  it("does not submit when credential is only whitespace", async () => {
    const loginMock = vi.fn().mockResolvedValue(undefined);
    const mockAuth = createMockAuth(loginMock);

    render(
      <AuthContext.Provider value={mockAuth}>
        <MemoryRouter>
          <LoginPage />
        </MemoryRouter>
      </AuthContext.Provider>
    );

    const input = screen.getByLabelText("Admin Credential");
    fireEvent.change(input, { target: { value: "   " } });

    const submitBtn = screen.getByTestId("login-submit-btn");
    expect(submitBtn).toBeDisabled();

    fireEvent.submit(input.closest("form")!);

    expect(loginMock).not.toHaveBeenCalled();
  });
});
