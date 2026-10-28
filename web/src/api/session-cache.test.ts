import { QueryClient } from "@tanstack/react-query";
import { expect, it } from "vite-plus/test";
import { getGetSetupStatusQueryKey, getGetCurrentSessionQueryKey } from "./generated/inbok";
import { clearAccountCache } from "./session-cache";

it("clears account data and credentials while retaining public setup status", async () => {
  const client = new QueryClient();
  const setup = { data: { status: "done" } };
  client.setQueryData(getGetSetupStatusQueryKey(), setup);
  client.setQueryData(getGetCurrentSessionQueryKey(), { data: { email: "old@example.com" } });
  client.setQueryData(["private-messages"], ["old account data"]);
  const mutation = client.getMutationCache().build(client, {
    mutationFn: async (_credentials: { password: string }) => undefined,
  });
  await mutation.execute({ password: "test-only-password" });

  clearAccountCache(client);

  expect(client.getQueryData(getGetSetupStatusQueryKey())).toEqual(setup);
  expect(client.getQueryData(getGetCurrentSessionQueryKey())).toBeUndefined();
  expect(client.getQueryData(["private-messages"])).toBeUndefined();
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  client.clear();
});

it("cancels an outstanding account read so it cannot repopulate the cache", async () => {
  const client = new QueryClient();
  let requestSignal: AbortSignal | undefined;
  const request = client
    .query({
      queryKey: ["private-messages"],
      queryFn: ({ signal }) => {
        requestSignal = signal;
        return new Promise<string[]>(() => {});
      },
    })
    .catch(() => undefined);

  clearAccountCache(client);
  await request;

  expect(requestSignal?.aborted).toBe(true);
  expect(client.getQueryData(["private-messages"])).toBeUndefined();
  client.clear();
});
