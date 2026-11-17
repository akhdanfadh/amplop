import { QueryClient } from "@tanstack/react-query";

// testQueryClient returns a new query cache for one test. A failed read
// is not tried again, which would wait 1 s and then 2 s in a test of a
// failure, as TanStack Query's testing guide sets it
// (https://tanstack.com/query/latest/docs/framework/react/guides/testing).
export function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
