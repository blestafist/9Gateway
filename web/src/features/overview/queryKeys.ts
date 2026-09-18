export const overviewQueryKeys = {
  all: ["overview"] as const,
  period: (period: string, after?: string, before?: string) =>
    [...overviewQueryKeys.all, period, after ?? "", before ?? ""] as const,
};
