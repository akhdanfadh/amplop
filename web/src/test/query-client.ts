import { QueryClient } from "@tanstack/react-query";
import { clearTabAccountData } from "@/features/auth/session-cache";

// The clients the harness made since the last endQueryClients. Only the
// browser tests' setup ends them; a unit test's go with its file's modules.
const made: QueryClient[] = [];

// testQueryClient returns a new query cache for one test. A failed read
// is not tried again, which would wait 1 s and then 2 s in a test of a
// failure, as TanStack Query's testing guide sets it
// (https://tanstack.com/query/latest/docs/framework/react/guides/testing).
export function testQueryClient(): QueryClient {
  return rememberQueryClient(new QueryClient({ defaultOptions: { queries: { retry: false } } }));
}

// rememberQueryClient keeps client, one the harness made for the running
// test, for endQueryClients to end, and returns it.
export function rememberQueryClient(client: QueryClient): QueryClient {
  made.push(client);
  return client;
}

// endQueryClients ends the member's data in every client the harness made
// since its last call (testQueryClient's, and the client of appRouter's
// router), as the end of a session does, cancels the reads still
// running, and forgets the clients. The browser tests' setup calls it
// after each test, once the page is gone.
//
// DESIGN: clearTabAccountData, not clearAccountCache, which also tells
// the browser's other tabs: a test has none, and the message, delivered
// later, would reach a channel the next test opens.
export async function endQueryClients(): Promise<void> {
  await Promise.all(
    made.splice(0).map(async (client) => {
      // Mail's collections, the undo bar, the folds, and whatever else
      // registered with onAccountCacheClear end here too.
      clearTabAccountData(client);
      // The setup status, which the line above keeps, goes as well.
      await client.cancelQueries();
      client.clear();
    }),
  );
}
