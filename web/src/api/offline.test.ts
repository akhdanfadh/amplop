import { MutationObserver, onlineManager, QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { getRequestPasswordResetLinkMutationOptions } from "./generated/amplop";
import { retryOptions } from "./retry";

const clients: QueryClient[] = [];
const request = { email: "ana@example.com" };
let wasOnline = true;

// Mount Query's real connectivity subscriptions so reconnect can expose queued writes.
function client(): QueryClient {
  const queryClient = new QueryClient({ defaultOptions: retryOptions });
  queryClient.mount();
  clients.push(queryClient);
  return queryClient;
}

beforeEach(() => {
  wasOnline = onlineManager.isOnline();
  vi.useFakeTimers();
});

afterEach(() => {
  for (const queryClient of clients) {
    queryClient.unmount();
    queryClient.clear();
  }
  clients.length = 0;
  onlineManager.setOnline(wasOnline);
});

describe("offline writes", () => {
  it("attempts immediately and stays failed after reconnection", async () => {
    onlineManager.setOnline(false);
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Offline"));
    vi.stubGlobal("fetch", fetch);
    const mutation = new MutationObserver(client(), getRequestPasswordResetLinkMutationOptions());
    const result = mutation.mutate({ data: request }).catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(0);

    expect(fetch).toHaveBeenCalledOnce();
    await expect(result).resolves.toMatchObject({ kind: "network" });
    expect(mutation.getCurrentResult()).toMatchObject({ status: "error", isPaused: false });

    onlineManager.setOnline(true);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(fetch).toHaveBeenCalledOnce();
    expect(mutation.getCurrentResult().status).toBe("error");
  });

  it("can succeed when the offline indicator is wrong about API reachability", async () => {
    onlineManager.setOnline(false);
    const body = { data: { status: "requested" } };
    const fetch = vi.fn<typeof globalThis.fetch>(async () => new Response(JSON.stringify(body)));
    vi.stubGlobal("fetch", fetch);
    const mutation = new MutationObserver(client(), getRequestPasswordResetLinkMutationOptions());

    await expect(mutation.mutate({ data: request })).resolves.toEqual(body);
    expect(mutation.getCurrentResult()).toMatchObject({ status: "success", isPaused: false });

    onlineManager.setOnline(true);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(fetch).toHaveBeenCalledOnce();
  });

  it("does not replay a write whose connection fails after sending", async () => {
    onlineManager.setOnline(true);
    let loseConnection: ((error: Error) => void) | undefined;
    const connection = new Promise<Response>((_resolve, reject) => {
      loseConnection = reject;
    });
    const fetch = vi.fn<typeof globalThis.fetch>(() => connection);
    vi.stubGlobal("fetch", fetch);
    const mutation = new MutationObserver(client(), getRequestPasswordResetLinkMutationOptions());
    const result = mutation.mutate({ data: request }).catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(0);
    expect(fetch).toHaveBeenCalledOnce();

    onlineManager.setOnline(false);
    if (!loseConnection) throw new Error("Fetch not started");
    loseConnection(new TypeError("Connection lost"));
    await vi.advanceTimersByTimeAsync(0);
    await expect(result).resolves.toMatchObject({ kind: "network" });

    onlineManager.setOnline(true);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(fetch).toHaveBeenCalledOnce();
    expect(mutation.getCurrentResult()).toMatchObject({ status: "error", isPaused: false });
  });
});
