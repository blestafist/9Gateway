export const systemQueryKeys = {
  all: ["system"] as const,
  info: () => [...systemQueryKeys.all, "info"] as const,
  readiness: () => [...systemQueryKeys.all, "readiness"] as const,
};
