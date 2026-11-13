import { createMemoryHistory } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { NotFound, RouteError, RoutePending } from "@/components/route-states";
import { getRouter } from "./router";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("getRouter", () => {
  it("gives every page the app's states, holding none of the first load's", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const router = getRouter();
    expect(router.options).toMatchObject({
      defaultPendingMs: 300,
      defaultPendingMinMs: 0,
      defaultPendingComponent: RoutePending,
      defaultErrorComponent: RouteError,
      defaultNotFoundComponent: NotFound,
    });

    // An address no guard reads the session for: the test page's own, /,
    // would ask whatever server answers the dev proxy.
    router.update({
      ...router.options,
      history: createMemoryHistory({ initialEntries: ["/nope"] }),
    });
    // Once the first load is done, a slow page's view stays at least 500 ms.
    await router.load();
    expect(router.options.defaultPendingMinMs).toBe(500);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
