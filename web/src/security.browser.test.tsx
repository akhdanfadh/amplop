import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { getGetCurrentSessionQueryKey, getGetTOTPStatusQueryKey } from "@/api/generated/inbok";
import { Route as Security } from "@/routes/_app/settings/security/index";

const fault = 500;

// The reads Security makes, each answering with data until its flag says
// to fail.
const failing = { session: false, totp: false };

function answer(path: string): Response {
  if (path.endsWith("/sessions/current"))
    return failing.session
      ? problem()
      : Response.json({
          data: {
            email: "ana@example.com",
            emailVerified: false,
            role: "member",
            totpRequired: false,
          },
        });
  if (path.endsWith("/totp"))
    return failing.totp
      ? problem()
      : Response.json({ data: { status: "off", backupCodesLeft: 0, backupCodesWaiting: false } });
  if (path.endsWith("/sessions"))
    return Response.json({
      data: {
        sessions: [
          {
            id: "0193c8a0-0000-7000-8000-000000000001",
            browser: "Firefox",
            os: "Linux",
            signedInAt: "2026-11-01T10:00:00Z",
            signedInFrom: "203.0.113.7",
            lastActiveAt: "2026-11-01T10:00:00Z",
            current: true,
          },
        ],
      },
    });
  return problem();
}

// problem is a server fault, as the API words one.
function problem(): Response {
  return Response.json(
    {
      type: "/api/v0/problems/internal-error",
      status: fault,
      title: "Internal Server Error",
      detail: "Something went wrong on our side. Try again in a moment.",
    },
    { status: fault, headers: { "Content-Type": "application/problem+json" } },
  );
}

// openSecurity renders Security's page in a router of its own (web/README.md
// "Checks and tests"), its reads answered by answer, and returns the query
// client, to refresh a read as a background check would.
async function openSecurity(): Promise<QueryClient> {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL) => Promise<Response>>(async (input) =>
      answer(new URL(input instanceof Request ? input.url : input, location.href).pathname),
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({ component: Outlet });
  const router = createRouter({
    routeTree: root.addChildren([
      createRoute({
        getParentRoute: () => root,
        path: "/settings/security",
        component: Security.options.component,
      }),
    ]),
    history: createMemoryHistory({ initialEntries: ["/settings/security"] }),
  });
  await render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await expect.element(page.getByText(/Firefox on Linux/)).toBeVisible();
  return queryClient;
}

afterEach(() => {
  failing.session = false;
  failing.totp = false;
  vi.unstubAllGlobals();
});

describe("Security", () => {
  it("says a failed session refresh failed, keeps the sections, and recovers on Try again", async () => {
    const queryClient = await openSecurity();
    const signOut = page.getByRole("button", { name: "Sign out of this browser" });
    await expect.element(signOut).toBeEnabled();

    failing.session = true;
    await queryClient.refetchQueries({ queryKey: getGetCurrentSessionQueryKey() });

    await expect.element(page.getByText(/Could not check your session\./)).toBeVisible();
    await expect.element(page.getByRole("heading", { name: "Email address" })).toBeVisible();
    await expect.element(signOut).toBeDisabled();

    failing.session = false;
    await userEvent.click(page.getByRole("button", { name: "Try again" }));

    await expect.element(page.getByText(/Could not check your session\./)).not.toBeInTheDocument();
    await expect.element(signOut).toBeEnabled();
  });

  it("says a failed two-factor refresh failed and keeps the row's last state", async () => {
    const queryClient = await openSecurity();
    await expect.element(page.getByText("Off. Signing in needs only your password.")).toBeVisible();

    failing.totp = true;
    await queryClient.refetchQueries({ queryKey: getGetTOTPStatusQueryKey() });

    await expect
      .element(page.getByText(/Could not check two-factor authentication\./))
      .toBeVisible();
    await expect.element(page.getByText("Off. Signing in needs only your password.")).toBeVisible();

    failing.totp = false;
    await userEvent.click(page.getByRole("button", { name: "Try again" }));

    await expect
      .element(page.getByText(/Could not check two-factor authentication\./))
      .not.toBeInTheDocument();
  });
});
