import type { ConversationRow, ListedAccount, PlaceCount } from "@/api/generated/model";

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
// answers a signed-in session. failNext makes the next conversation list
// request answer status, and failOlder every request for an older page
// until it is cleared; signedOut answers everything 401, as after the
// session ended. requests records every path and search it was asked.
export type MailApi = {
  conversations: ConversationRow[];
  accounts: ListedAccount[];
  places: PlaceCount[];
  requests: string[];
  failNext: number | undefined;
  failOlder: number | undefined;
  signedOut: boolean;
  fetch: (input: RequestInfo | URL) => Promise<Response>;
};

// mailApi returns a MailApi holding conversations and accounts.
export function mailApi(conversations: ConversationRow[], accounts: ListedAccount[]): MailApi {
  const api: MailApi = {
    conversations,
    accounts,
    places: [],
    requests: [],
    failNext: undefined,
    failOlder: undefined,
    signedOut: false,
    fetch: async (input) => {
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
      if (path === "/api/v0/conversations/new-count") {
        const after = Date.parse(search.get("after") ?? "");
        const count = inPlace(api.conversations, search).filter((c) => Date.parse(c.date) > after);
        return Response.json({ data: { count: count.length } });
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
