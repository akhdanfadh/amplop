import { describe, expect, it } from "vite-plus/test";
import { NotFound, RouteError, RoutePending } from "@/components/route-states";
import { getRouter } from "./router";

describe("getRouter", () => {
  it("gives every page the app's states, holding none of the first load's", async () => {
    const router = getRouter();
    expect(router.options).toMatchObject({
      defaultPendingMs: 300,
      defaultPendingMinMs: 0,
      defaultPendingComponent: RoutePending,
      defaultErrorComponent: RouteError,
      defaultNotFoundComponent: NotFound,
    });

    // Once the first load is done, a slow page's view stays at least 500 ms.
    await router.load();
    expect(router.options.defaultPendingMinMs).toBe(500);
  });
});
