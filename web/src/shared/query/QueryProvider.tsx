import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { adminQueryClient } from "./queryClient";

export interface AdminQueryProviderProps {
  children: React.ReactNode;
  client?: QueryClient;
}

export const AdminQueryProvider: React.FC<AdminQueryProviderProps> = ({
  children,
  client = adminQueryClient,
}) => {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
};
