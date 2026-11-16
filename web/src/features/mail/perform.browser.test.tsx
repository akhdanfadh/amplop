import { QueryClient } from "@tanstack/react-query";
import { createLiveQueryCollection } from "@tanstack/react-db";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { closeToasts, Toaster } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import type { ActionMail } from "./actions";
import { mailData, type Conversation } from "./conversations";
import { perform } from "./perform";
import { placeWhere } from "./rules";
import { labelWords } from "./words";

let current: QueryClient | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

// Labels' ids, as List Places gives them.
const work = "00000000-0000-4000-8000-0000000000a1";
const travel = "00000000-0000-4000-8000-0000000000a2";

// openInbox serves api, holding the answer to the held-th request to
// Change Conversations (counting from 0) until released, and returns the
// mail of a client with an open Inbox list, as a mail page has it, beside
// the app's toasts.
async function openInbox(api: MailApi, held: number) {
  let changes = 0;
  const gate: { open?: () => void } = {};
  const waiting = new Promise<void>((done) => {
    gate.open = done;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : String(input);
      if (new URL(url, location.href).pathname === "/api/v0/conversations/changes") {
        if (changes++ === held) await waiting;
      }
      return api.fetch(input, init);
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const { conversations, placeCounts } = mailData(client);
  const list = createLiveQueryCollection((q) =>
    q
      .from({ c: conversations })
      .where(({ c }) => placeWhere(c, "inbox", undefined))
      .orderBy(({ c }) => c.dateMs, "desc")
      .limit(50),
  );
  list.subscribeChanges(() => {});
  placeCounts.subscribeChanges(() => {});
  await Promise.all([list.preload(), placeCounts.preload()]);
  await render(<Toaster />);
  const mail: ActionMail = { client, conversations, placeCounts };
  return { mail, release: () => gate.open?.(), sent: () => changes };
}

// remove runs taking conversations out of labels, named names, as the
// picking bar's Labels does.
function remove(
  mail: ActionMail,
  conversations: Conversation[],
  labels: string[],
  names: string[],
) {
  return perform({
    mail,
    conversations,
    act: {
      changes: [{ ids: conversations.map((c) => c.id), remove: labels }],
      words: labelWords([], names),
    },
    from: "picked",
  });
}

describe("perform", () => {
  it("says a slow batch's failure only of what no later action changed again", async () => {
    // 26 conversations: two requests, the second held.
    const api = mailApi(
      Array.from({ length: 26 }, (_, n) => conversation(n + 1, { labelIds: [work, travel] })),
      [listedAccount(1, "ana@example.com")],
    );
    const { mail, release, sent } = await openInbox(api, 1);
    const all = [...mail.conversations.values()];
    // In the first request, which answers at once: refused.
    const [first] = all;
    api.refuse[first.id] = "failed";

    const slow = remove(mail, all, [work, travel], ["Work", "Travel"]);
    await expect.poll(sent).toBe(2);
    delete api.refuse[first.id];
    // Taken out of Work again while the second request waits: that works.
    await remove(mail, [first], [work], ["Work"]);
    release();
    await slow;
    const error = page.getByText("Couldn't change labels on 1 conversation");
    await expect.element(error).toBeVisible();

    // Travel, the one change still failed, now works too.
    await remove(mail, [first], [travel], ["Travel"]);

    await expect.element(error).not.toBeInTheDocument();
  });
});
