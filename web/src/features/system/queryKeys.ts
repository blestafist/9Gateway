export const systemQueryKeys = {
  all: ["system"] as const,
  readiness: () => [...systemQueryKeys.all, "readiness"] as const,
};
