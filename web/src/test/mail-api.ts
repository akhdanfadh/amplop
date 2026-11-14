import { RenameConnectedAccountBody } from "@/api/generated/model/renameConnectedAccountBody.zod";
import {
  ConversationActionBody,
  type ConversationRow,
  type ListedAccount,
  type PlaceCount,
} from "@/api/generated/model";

const notFound = 404;
const unauthorized = 401;

// A connected account as the API lists it, fully synced.
export function listedAccount(id: number, email: string, color = id): ListedAccount {
  return {
    id,
    email,
    color,
    name: null,
    createdAt: "2026-11-01T08:00:00Z",
    needsReconnectSince: null,
    provider: "gmail",
    status: "usable",
    sync: {
      status: "complete",
      failure: null,
      failedAt: null,
      lastSyncedAt: "2026-11-13T08:00:00Z",
      messagesSoFar: null,
    },
  };
}

// conversation returns an Inbox conversation of account 1, numbered n,
// dated n minutes after 08:00 on 13 November 2026; changes override any
// field.
export function conversation(n: number, changes: Partial<ConversationRow> = {}): ConversationRow {
  return {
    id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
    accountId: 1,
    date: new Date(Date.UTC(2026, 10, 13, 8, n)).toISOString(),
    subject: `Subject ${n}`,
    preview: `Preview ${n}`,
    latestFrom: [{ name: "Bo Tan", address: "bo@example.com" }],
    participants: [{ name: "Bo Tan", address: "bo@example.com" }],
    messageCount: 1,
    labelIds: [],
    inbox: true,
    sent: false,
    allMail: true,
    spam: false,
    trash: false,
    starred: false,
    unread: false,
    ...changes,
  };
}

// MailApi is a fake inbok API for mail pages: it pages conversations as
// List Conversations does, counts new ones, lists accounts and places, and
// answers a signed-in session; it stars and unstars conversations. failNext
// makes the next conversation list request answer status, failOlder every
// request for an older page until it is cleared, failAction the
// next action, and refuse refuses an action on a conversation, by ID, with
// a reason; signedOut answers everything 401, as after the session ended.
// It renames an account as Rename Account does.
// requests records every path and search it was asked; actions every
// action body.
export type MailApi = {
  conversations: ConversationRow[];
  accounts: ListedAccount[];
  places: PlaceCount[];
  requests: string[];
  actions: { action: string; ids: string[] }[];
  failNext: number | undefined;
  failOlder: number | undefined;
  failAction: number | undefined;
  signedOut: boolean;
  refuse: Record<string, "not-found" | "needs-reconnect" | "failed">;
  fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
};

// mailApi returns a MailApi holding conversations and accounts.
export function mailApi(conversations: ConversationRow[], accounts: ListedAccount[]): MailApi {
  const api: MailApi = {
    conversations,
    accounts,
    places: [],
    requests: [],
    actions: [],
    failNext: undefined,
    failOlder: undefined,
    failAction: undefined,
    signedOut: false,
    refuse: {},
    fetch: async (input, init) => {
      const url = new URL(input instanceof Request ? input.url : String(input), location.href);
      api.requests.push(url.pathname + url.search);
      if (api.signedOut) return problem(unauthorized, "/api/v0/problems/unauthorized");
      const { pathname: path, searchParams: search } = url;
      if (path === "/api/v0/conversations") {
        if (api.failNext !== undefined) {
          const status = api.failNext;
          api.failNext = undefined;
          return problem(status);
        }
        if (api.failOlder !== undefined && search.has("before")) return problem(api.failOlder);
        return Response.json({ data: { conversations: list(api.conversations, search) } });
      }
      if (path === "/api/v0/conversations/actions") {
        const { action, ids } = ConversationActionBody.parse(
          JSON.parse(typeof init?.body === "string" ? init.body : "{}"),
        );
        api.actions.push({ action, ids });
        if (api.failAction !== undefined) {
          const status = api.failAction;
          api.failAction = undefined;
          return problem(status);
        }
        const refused = ids.filter((id) => id in api.refuse);
        const changed = api.conversations.filter(
          (c) => ids.includes(c.id) && !refused.includes(c.id),
        );
        for (const c of changed) {
          if (action === "star" || action === "unstar") c.starred = action === "star";
        }
        return Response.json({
          data: {
            conversations: changed,
            refused: refused.map((id) => ({ id, reason: api.refuse[id] })),
            undo: action === "star" ? "unstar" : "star",
          },
        });
      }
      if (path === "/api/v0/conversations/new-count") {
        const after = Date.parse(search.get("after") ?? "");
        const count = inPlace(api.conversations, search).filter((c) => Date.parse(c.date) > after);
        return Response.json({ data: { count: count.length } });
      }
      if (path.startsWith("/api/v0/conversations/")) {
        const found = api.conversations.find((c) => path.endsWith(c.id));
        return found
          ? Response.json({ data: { conversation: found, messages: [] } })
          : problem(notFound);
      }
      const renamed = /^\/api\/v0\/connected-accounts\/(\d+)$/.exec(path);
      if (renamed && init?.method === "PATCH") {
        const account = api.accounts.find((a) => a.id === Number(renamed[1]));
        if (!account) return problem(notFound);
        const { name } = RenameConnectedAccountBody.parse(
          JSON.parse(typeof init.body === "string" ? init.body : "{}"),
        );
        account.name = name.trim() === "" ? null : name.trim();
        return Response.json({ data: { account } });
      }
      if (path === "/api/v0/places") {
        return Response.json({ data: { places: api.places } });
      }
      if (path === "/api/v0/connected-accounts") {
        return Response.json({ data: { accounts: api.accounts, syncStopped: false } });
      }
      if (path === "/api/v0/sessions/current") {
        return Response.json({
          data: {
            email: "ana@example.com",
            emailVerified: true,
            role: "member",
            totpRequired: false,
          },
        });
      }
      return problem(notFound);
    },
  };
  return api;
}

// problem returns an error response with status, and type.
function problem(status: number, type = "about:blank"): Response {
  return Response.json(
    { type, status, title: "Failed", detail: "It failed." },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// inPlace returns the conversations in search's place and account.
function inPlace(conversations: ConversationRow[], search: URLSearchParams): ConversationRow[] {
  const place = search.get("place");
  const account = search.get("account");
  return conversations.filter(
    (c) =>
      (account === null || c.accountId === Number(account)) &&
      (place === "inbox"
        ? c.inbox
        : place === "spam"
          ? c.spam
          : place === "trash"
            ? c.trash
            : place === "all"
              ? c.allMail
              : c.labelIds.includes(place ?? "")),
  );
}

// list returns one page as List Conversations does: newest first, then by
// ID descending, after before and beforeId, at most limit.
function list(conversations: ConversationRow[], search: URLSearchParams): ConversationRow[] {
  const before = search.get("before");
  const beforeId = search.get("beforeId") ?? "";
  const limit = Number(search.get("limit") ?? 50);
  // oxlint-disable-next-line unicorn/no-array-sort -- a copy; tsconfig's lib has no toSorted.
  const sorted = [...inPlace(conversations, search)].sort(
    (a, b) => Date.parse(b.date) - Date.parse(a.date) || (a.id < b.id ? 1 : -1),
  );
  const after = before
    ? sorted.filter((c) => {
        const date = Date.parse(c.date);
        return date < Date.parse(before) || (date === Date.parse(before) && c.id < beforeId);
      })
    : sorted;
  return after.slice(0, limit);
}
