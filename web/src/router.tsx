import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

// TanStack Start calls getRouter to create the app's router.
export function getRouter() {
  return createRouter({ routeTree });
}
