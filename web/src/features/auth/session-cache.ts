import { hashKey, type QueryClient } from "@tanstack/react-query";
import { getGetSetupStatusQueryKey } from "@/api/generated/inbok";

// clearAccountCache removes account data and mutation variables, retaining public setup status.
export function clearAccountCache(client: QueryClient): void {
  const setupHash = hashKey(getGetSetupStatusQueryKey());
  client.removeQueries({ predicate: (query) => query.queryHash !== setupHash });
  client.getMutationCache().clear();
}
