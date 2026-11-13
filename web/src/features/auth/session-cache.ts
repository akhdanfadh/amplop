import { hashKey, type QueryClient } from "@tanstack/react-query";
import { getGetSetupStatusQueryKey } from "@/api/generated/inbok";
import { closeToasts } from "@/components/ui/toast";
import { forgetMailHref } from "@/features/mail/places";

// clearAccountCache ends what the app holds for one member: it removes
// account data and mutation variables, keeping the public setup status,
// closes every toast, and sends Back to mail to the Inbox. Every sign-in,
// sign-out, and ended session calls it, so whatever else holds a member's
// data, e.g. a TanStack DB collection, is cleared here too.
export function clearAccountCache(client: QueryClient): void {
  closeToasts();
  forgetMailHref();
  const setupHash = hashKey(getGetSetupStatusQueryKey());
  client.removeQueries({ predicate: (query) => query.queryHash !== setupHash });
  client.getMutationCache().clear();
}
