import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Route as appRoot } from "@/routes/__root";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { mailData, toConversation } from "./conversations";
import { Reader } from "./reader";

let current: QueryClient | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

// app renders the reading pane for id on api, with row already in the
// list's collection when given.
async function app(api: MailApi, id: string, row?: ReturnType<typeof conversation>) {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  if (row) await mailData(client).conversations.utils.writeUpsert(toConversation(row));
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => (
      <Reader conversationId={id} back={{ to: "/mail/$place", params: { place: "inbox" } }} />
    ),
  });
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory(),
    context: { queryClient: client },
    Wrap: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  await render(<RouterProvider router={router} />);
  void appRoot;
}

describe("Reader", () => {
  it("is Page not found for a listed conversation that no longer exists", async () => {
    const gone = conversation(1);
    await app(mailApi([], [listedAccount(1, "ana@example.com")]), gone.id, gone);
    await expect.element(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  });

  it("shows each sender's address beside the name", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const message = {
      id: "00000000-0000-4000-8000-000000000901",
      date: row.date,
      subject: row.subject,
      preview: "Hello",
      from: [{ name: "Bo Tan", address: "bo@example.com" }],
      to: [{ name: "", address: "ana@example.com" }],
      cc: [],
      replyTo: [],
      labelIds: [],
      spam: false,
      trash: false,
      starred: false,
      unread: false,
    };
    const detail = Response.json({ data: { conversation: row, messages: [message] } });
    const fetch = api.fetch;
    api.fetch = async (input) =>
      (input instanceof Request ? input.url : input.toString()).includes(`/${row.id}`)
        ? detail.clone()
        : fetch(input);
    await app(api, row.id);
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    await expect.element(page.getByText("To me")).toBeVisible();
  });
});
