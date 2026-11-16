import { RenameConnectedAccountBody } from "@/api/generated/model/renameConnectedAccountBody.zod";
import {
  ConversationActionBody,
  ConversationChangesBody,
  type ConversationChange,
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
// answers a signed-in session; it applies actions and changes as the
// server's rows show them, answers each with the changes that undo it, and
// with placesAfter as the places, when set, and as List Places then.
// failNext
// makes the next conversation list request answer status, failOlder every
// request for an older page until it is cleared, failAction the
// next action, and refuse refuses an action on a conversation, by ID, with
// a reason; signedOut answers everything 401, as after the session ended.
// It starts an account's sync, recording the account in syncs, unless
// refuseSync names a status and problem type for that account;
// failAccounts makes every accounts read answer that status; it renames
// an account as Rename Account does, offers Gmail, and says setup is done.
// It searches conversations by their subjects, each word anywhere in it,
// as Search Conversations answers, a page at a time, each page after the
// first starting searchShift results early, as a ranking a sync shifted
// between pages would, or answers failSearch's status, with Retry-After
// when it is 429 or 503. requests records every path and
// search it was asked; actions every action body.
export type MailApi = {
  conversations: ConversationRow[];
  accounts: ListedAccount[];
  places: PlaceCount[];
  placesAfter: PlaceCount[] | undefined;
  requests: string[];
  actions: { action: string; ids: string[] }[];
  failNext: number | undefined;
  failOlder: number | undefined;
  failAction: number | undefined;
  signedOut: boolean;
  refuse: Record<string, "not-found" | "needs-reconnect" | "failed">;
  syncs: number[];
  failAccounts: number | undefined;
  refuseSync: Record<number, { status: number; type: string }>;
  failSearch: number | undefined;
  searchShift: number;
  fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
};

// mailApi returns a MailApi holding conversations and accounts.
export function mailApi(conversations: ConversationRow[], accounts: ListedAccount[]): MailApi {
  const api: MailApi = {
    conversations,
    accounts,
    places: [],
    placesAfter: undefined,
    requests: [],
    actions: [],
    failNext: undefined,
    failOlder: undefined,
    failAction: undefined,
    signedOut: false,
    refuse: {},
    syncs: [],
    failAccounts: undefined,
    refuseSync: {},
    failSearch: undefined,
    searchShift: 0,
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
        return answerChanges(api, ids, (c) => Object.assign(c, actionRows[action]));
      }
      if (path === "/api/v0/conversations/changes") {
        const { changes } = ConversationChangesBody.parse(
          JSON.parse(typeof init?.body === "string" ? init.body : "{}"),
        );
        api.actions.push({ action: "changes", ids: changes.flatMap((c) => c.ids) });
        if (api.failAction !== undefined) {
          const status = api.failAction;
          api.failAction = undefined;
          return problem(status);
        }
        return answerChanges(
          api,
          changes.flatMap((c) => c.ids),
          (c) => {
            const change = changes.find((one) => one.ids.includes(c.id));
            if (change) applyChange(c, change);
          },
        );
      }
      if (path === "/api/v0/conversations/new-count") {
        const after = Date.parse(search.get("after") ?? "");
        const count = inPlace(api.conversations, search).filter((c) => Date.parse(c.date) > after);
        return Response.json({ data: { count: count.length } });
      }
      if (path === "/api/v0/conversations/search") {
        if (api.failSearch !== undefined) {
          return problem(api.failSearch, "about:blank", { "Retry-After": "1" });
        }
        return Response.json({ data: searched(api.conversations, search, api.searchShift) });
      }
      if (path.startsWith("/api/v0/conversations/")) {
        const found = api.conversations.find((c) => path.endsWith(c.id));
        return found
          ? Response.json({ data: { conversation: found, messages: [], providerUrl: null } })
          : problem(notFound);
      }
      const sync = /^\/api\/v0\/connected-accounts\/(\d+)\/sync$/.exec(path);
      if (sync && init?.method === "POST") {
        const id = Number(sync[1]);
        const refusal = api.refuseSync[id];
        if (refusal) return problem(refusal.status, refusal.type);
        api.syncs.push(id);
        const account = api.accounts.find((a) => a.id === id);
        return account ? Response.json({ data: account.sync }) : problem(notFound);
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
      // The root asks whether inbok still needs its first administrator.
      if (path === "/api/v0/setup") {
        return Response.json({ data: { status: "done" } });
      }
      if (path === "/api/v0/connected-accounts/providers") {
        return Response.json({ data: { offered: ["gmail"] } });
      }
      if (path === "/api/v0/places") {
        return Response.json({ data: { places: api.places } });
      }
      if (path === "/api/v0/connected-accounts") {
        if (api.failAccounts !== undefined) return problem(api.failAccounts);
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

// The flags each action leaves on a row, as the server's rows show them.
const actionRows: Record<ConversationActionBody["action"], Partial<ConversationRow>> = {
  read: { unread: false },
  unread: { unread: true },
  archive: { inbox: false },
  unarchive: { inbox: true },
  star: { starred: true },
  unstar: { starred: false },
  trash: { trash: true, inbox: false, allMail: false, sent: false, spam: false, labelIds: [] },
  untrash: { trash: false, inbox: true, allMail: true },
  spam: { spam: true, inbox: false, allMail: false, sent: false, labelIds: [] },
  unspam: { spam: false, inbox: true, allMail: true },
};

// applyChange applies change to row as it is.
function applyChange(row: ConversationRow, change: ConversationChange): void {
  const add = change.add ?? [];
  const remove = change.remove ?? [];
  for (const view of ["inbox", "spam", "trash"] as const) {
    if (add.includes(view)) row[view] = true;
    if (remove.includes(view)) row[view] = false;
  }
  row.allMail = !row.spam && !row.trash;
  const labels = add.filter((place) => !["inbox", "spam", "trash"].includes(place));
  row.labelIds = [...row.labelIds.filter((label) => !remove.includes(label)), ...labels];
  if (change.starred !== undefined) row.starred = change.starred;
  if (change.unread !== undefined) row.unread = change.unread;
}

// placesOf returns the places of row a change reaches.
function placesOf(row: ConversationRow): string[] {
  return [...(["inbox", "spam", "trash"] as const).filter((view) => row[view]), ...row.labelIds];
}

// undoOf returns the change that takes after back to before, if they
// differ, as the server computes it.
function undoOf(before: ConversationRow, after: ConversationRow): ConversationChange | undefined {
  const was = placesOf(before);
  const is = placesOf(after);
  const change: ConversationChange = { ids: [after.id] };
  const add = was.filter((place) => !is.includes(place));
  const remove = is.filter((place) => !was.includes(place));
  if (add.length > 0) change.add = add;
  if (remove.length > 0) change.remove = remove;
  if (before.starred !== after.starred) change.starred = before.starred;
  if (before.unread !== after.unread) change.unread = before.unread;
  return Object.keys(change).length > 1 ? change : undefined;
}

// answerChanges applies apply to api's conversations of ids, but those
// api refuses, and answers as the API does.
function answerChanges(
  api: MailApi,
  ids: string[],
  apply: (row: ConversationRow) => void,
): Response {
  const refused = ids.filter((id) => id in api.refuse);
  const changed = api.conversations.filter((c) => ids.includes(c.id) && !refused.includes(c.id));
  const undo: ConversationChange[] = [];
  for (const row of changed) {
    const before = { ...row, labelIds: [...row.labelIds] };
    apply(row);
    const change = undoOf(before, row);
    if (change) undo.push(change);
  }
  if (api.placesAfter) api.places = api.placesAfter;
  return Response.json({
    data: {
      conversations: changed,
      refused: refused.map((id) => ({ id, reason: api.refuse[id] })),
      undo,
      places: api.placesAfter,
    },
  });
}

// problem returns an error response with status, and type.
function problem(
  status: number,
  type = "about:blank",
  headers: Record<string, string> = {},
): Response {
  return Response.json(
    { type, status, title: "Failed", detail: "It failed." },
    { status, headers: { "Content-Type": "application/problem+json", ...headers } },
  );
}

// viewsOf returns the places row is in, as the lists filter: its views,
// then its labels.
function viewsOf(row: ConversationRow): string[] {
  const views: [string, boolean][] = [
    ["inbox", row.inbox],
    ["starred", row.starred && row.allMail],
    ["sent", row.sent],
    ["all", row.allMail],
    ["spam", row.spam],
    ["trash", row.trash],
  ];
  return [...views.filter(([, inIt]) => inIt).map(([view]) => view), ...row.labelIds];
}

// searched answers a search as Search Conversations does, of conversations
// whose subject holds every word of search's q, anywhere, ignoring case,
// in the places it names (none: All mail): each with its subject as the
// match, the words marked where they first appear, newest first.
function searched(conversations: ConversationRow[], search: URLSearchParams, shift: number) {
  const words = (search.get("q") ?? "").toLowerCase().split(/\s+/).filter(Boolean);
  const places = search.getAll("place");
  const account = search.get("account");
  const results = conversations
    .filter((c) => words.every((word) => c.subject.toLowerCase().includes(word)))
    .filter((c) => account === null || c.accountId === Number(account))
    .filter((c) =>
      (places.length > 0 ? places : ["all"]).some((place) => viewsOf(c).includes(place)),
    )
    .map((c) => ({
      conversation: c,
      match: {
        messageId: c.id.replace("00000000-0000-4000-8000", "11111111-1111-4111-8111"),
        date: c.date,
        from: c.latestFrom,
        subject: c.subject,
        subjectMatches: words.flatMap((word) => {
          const start = c.subject.toLowerCase().indexOf(word);
          return start === -1 ? [] : [{ start, length: word.length }];
        }),
        preview: c.preview,
        previewMatches: [],
      },
    }));
  const page = Number(search.get("page") ?? "1");
  const limit = Number(search.get("limit") ?? "50");
  const start = Math.max(0, (page - 1) * limit - (page > 1 ? shift : 0));
  return { results: results.slice(start, start + limit), total: results.length };
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
