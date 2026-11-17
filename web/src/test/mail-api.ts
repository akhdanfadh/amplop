import * as z from "zod";
import { RenameConnectedAccountBody } from "@/api/generated/model/renameConnectedAccountBody.zod";
import {
  ConversationActionBody,
  ConversationChangesBody,
  type ConversationChange,
  type ConversationMessage,
  type ConversationRow,
  type ListedAccount,
  type MessageContent,
  type OutgoingMessage,
  type PlaceCount,
  type ProblemType,
} from "@/api/generated/model";
import { data, fakeServer, memberSession, problem, type Asked, type Server } from "./server";

const created = 201;
const notFound = 404;
const invalid = 422;
const busy = 503;

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
    allowedActions: ["sync", "send", "change-mail", "reconnect", "rename", "disconnect"],
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

// message returns the one message of row, read, from Bo Tan to
// ana@example.com, with row's date and subject, as Get Conversation lists
// it; changes override any field.
export function message(
  row: ConversationRow,
  changes: Partial<ConversationMessage> = {},
): ConversationMessage {
  return {
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
    sent: false,
    starred: false,
    unread: false,
    answers: null,
    attachments: false,
    ...changes,
  };
}

// A message's content with nothing to show, as Get Message Content
// answers it.
export const noContent: MessageContent = { remote: "none", attachments: [], bodies: [] };

// outgoingMessage returns a message the server saved, waiting to be sent:
// "Lunch", from studio@example.com, account 7, to ana@example.com, saved
// at 08:00 on 16 November 2026; changes override any field.
export function outgoingMessage(changes: Partial<OutgoingMessage> = {}): OutgoingMessage {
  return {
    id: "019a0000-0000-7000-8000-0000000000aa",
    status: "waiting",
    failure: null,
    from: "studio@example.com",
    connectedAccountId: 7,
    to: ["ana@example.com"],
    cc: [],
    bcc: [],
    subject: "Lunch",
    body: "Noon?",
    createdAt: "2026-11-16T08:00:00Z",
    sentAt: null,
    sentAgainAs: null,
    dismissedAt: null,
    outstanding: true,
    reply: false,
    allowedActions: [],
    ...changes,
  };
}

// MailApi is a fake inbok API for mail pages, its routes on server, where
// a test adds its own: it pages conversations as
// List Conversations does, counts new ones, lists accounts and places, and
// answers a signed-in session; it applies actions and changes as the
// server's rows show them, answers each with the changes that undo it, and
// with placesAfter as the places, when set, and as List Places then.
// failNext
// makes the next conversation list request answer status, failOlder every
// request for an older page until it is cleared, failAction the
// next action, and refuse refuses an action on a conversation, by ID, with
// a reason; signedOut ends server's session, so everything answers 401.
// It starts an account's sync, recording the account in syncs, unless
// refuseSync names a status and problem type for that account;
// failAccounts makes every accounts read answer that status; it renames
// an account as Rename Account does, offers Gmail, lists an empty Outbox,
// and says setup is done.
// It searches conversations by their subjects, each word anywhere in it,
// as Search Conversations answers, a page at a time, each page after the
// first starting searchShift results early, as a ranking a sync shifted
// between pages would, and each page in searchDropped empty, as when
// PostgreSQL no longer holds its hits, or answers failSearch's status,
// with Retry-After when it is 429 or 503. requests records every path and
// search it was asked; actions every action body, and changes every
// change body's changes. Label writes change places as Gmail would, and
// labelWrites records each, method and path; refuseLabel, while set,
// refuses the next as a 422 at body.name, or with a status alone, and
// deleteOnly, while set, deletes that many labels of a delete before it
// fails, as a delete Gmail stopped part way does. It answers a
// conversation's read with the messages listed for its ID in messages,
// none unless a test lists them, and every message's content as one with
// nothing to show.
export type MailApi = {
  conversations: ConversationRow[];
  messages: Record<string, ConversationMessage[]>;
  accounts: ListedAccount[];
  places: PlaceCount[];
  placesAfter: PlaceCount[] | undefined;
  readonly requests: string[];
  actions: { action: string; ids: string[] }[];
  changes: ConversationChange[][];
  failNext: number | undefined;
  failOlder: number | undefined;
  failAction: number | undefined;
  signedOut: boolean;
  refuse: Record<string, "not-found" | "needs-reconnect" | "failed">;
  syncs: number[];
  failAccounts: number | undefined;
  refuseSync: Record<number, { status: number; type: ProblemType }>;
  failSearch: number | undefined;
  searchShift: number;
  searchDropped: number[];
  labelWrites: string[];
  refuseLabel: string | number | undefined;
  deleteOnly: number | undefined;
  server: Server;
};

// mailApi returns a MailApi holding conversations and accounts.
export function mailApi(conversations: ConversationRow[], accounts: ListedAccount[]): MailApi {
  const server = fakeServer(memberSession);
  const api: MailApi = {
    conversations,
    messages: {},
    accounts,
    places: [],
    placesAfter: undefined,
    actions: [],
    changes: [],
    failNext: undefined,
    failOlder: undefined,
    failAction: undefined,
    refuse: {},
    syncs: [],
    failAccounts: undefined,
    refuseSync: {},
    failSearch: undefined,
    searchShift: 0,
    searchDropped: [],
    labelWrites: [],
    refuseLabel: undefined,
    deleteOnly: undefined,
    get signedOut() {
      return server.session === undefined;
    },
    set signedOut(out) {
      server.session = out ? undefined : memberSession;
    },
    get requests() {
      return server.requests.map((asked) => asked.path);
    },
    server,
  };

  server.on("GET", "/api/v0/conversations", ({ url }) => {
    if (api.failNext !== undefined) {
      const status = api.failNext;
      api.failNext = undefined;
      return problem(status);
    }
    if (api.failOlder !== undefined && url.searchParams.has("before")) {
      return problem(api.failOlder);
    }
    return data({ conversations: list(api.conversations, url.searchParams) });
  });
  server.on("POST", "/api/v0/conversations/actions", ({ body }) => {
    const { action, ids } = ConversationActionBody.parse(body ?? {});
    api.actions.push({ action, ids });
    if (api.failAction !== undefined) {
      const status = api.failAction;
      api.failAction = undefined;
      return problem(status);
    }
    return answerChanges(api, ids, (c) => Object.assign(c, actionRows[action]));
  });
  server.on("POST", "/api/v0/conversations/changes", ({ body }) => {
    const { changes } = ConversationChangesBody.parse(body ?? {});
    api.actions.push({ action: "changes", ids: changes.flatMap((c) => c.ids) });
    api.changes.push(changes);
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
  });
  server.on("GET", "/api/v0/conversations/new-count", ({ url }) => {
    const after = Date.parse(url.searchParams.get("after") ?? "");
    const count = inPlace(api.conversations, url.searchParams).filter(
      (c) => Date.parse(c.date) > after,
    );
    return data({ count: count.length });
  });
  server.on("GET", "/api/v0/conversations/search", ({ url }) => {
    if (api.failSearch !== undefined)
      return problem(api.failSearch, "about:blank", { retryAfter: 1 });
    return data(searched(api.conversations, url.searchParams, api.searchShift, api.searchDropped));
  });
  server.on("GET", "/api/v0/conversations/{id}", ({ params }) => {
    const found = api.conversations.find((c) => c.id === params.id);
    return found
      ? data({ conversation: found, messages: api.messages[found.id] ?? [], providerUrl: null })
      : problem(notFound);
  });
  server.on("GET", "/api/v0/messages/{id}/content", () => data(noContent));
  server.on("POST", "/api/v0/connected-accounts/{id}/sync", ({ params }) => {
    const id = Number(params.id);
    const refusal = api.refuseSync[id];
    if (refusal) return problem(refusal.status, refusal.type);
    api.syncs.push(id);
    const account = api.accounts.find((a) => a.id === id);
    return account ? data(account.sync) : problem(notFound);
  });
  server.on("PATCH", "/api/v0/connected-accounts/{id}", ({ params, body }) => {
    const account = api.accounts.find((a) => a.id === Number(params.id));
    if (!account) return problem(notFound);
    const { name } = RenameConnectedAccountBody.parse(body ?? {});
    account.name = name.trim() === "" ? null : name.trim();
    return data({ account });
  });
  server.on("POST", "/api/v0/labels", (asked) => answerLabel(api, asked));
  server.on("PATCH", "/api/v0/labels/{id}", (asked) => answerLabel(api, asked));
  server.on("DELETE", "/api/v0/labels/{id}", (asked) => answerLabel(api, asked));
  server.on("GET", "/api/v0/connected-accounts/providers", () => data({ offered: ["gmail"] }));
  server.on("GET", "/api/v0/places", () => data({ places: api.places }));
  // The mail sidebar counts the Outbox's messages.
  server.on("GET", "/api/v0/outgoing-messages", () => data({ messages: [] }));
  server.on("GET", "/api/v0/connected-accounts", () => {
    if (api.failAccounts !== undefined) return problem(api.failAccounts);
    return data({ accounts: api.accounts, syncStopped: false });
  });
  return api;
}

// answerLabel answers a label write, asked, on api's places and
// conversations, as the server and Gmail would.
function answerLabel(api: MailApi, { method, url, params, body }: Asked): Response {
  api.labelWrites.push(`${method} ${url.pathname}`);
  const refusal = api.refuseLabel;
  if (refusal !== undefined) {
    api.refuseLabel = undefined;
    if (typeof refusal === "number") return problem(refusal);
    return problem(invalid, "/api/v0/problems/validation-error", {
      title: "Unprocessable Entity",
      detail: "Check the values you entered.",
      errors: [{ location: "body.name", message: refusal }],
    });
  }
  const sent = LabelWrite.parse(body ?? {});
  const found = api.places.find((p) => p.place === params.id && p.name !== null);
  if (method === "POST") {
    const made = {
      accountId: sent.accountId ?? 0,
      place: crypto.randomUUID(),
      name: sent.name ?? "",
      parent: sent.parent ?? null,
      unread: 0,
    };
    api.places = [...api.places, made];
    return data(labelResource(made), created);
  }
  if (!found) return problem(notFound, "/api/v0/problems/label-not-found");
  if (method === "PATCH") {
    const edited = { ...found, name: sent.name ?? found.name, parent: sent.parent ?? null };
    api.places = api.places.map((p) => (p === found ? edited : p));
    return data(labelResource(edited));
  }
  // Deepest first, the label itself last, as Gmail's code deletes.
  const tree = [found.place];
  for (let i = 0; i < tree.length; i++) {
    for (const p of api.places) if (p.parent === tree[i] && p.name !== null) tree.push(p.place);
  }
  const deepestFirst: string[] = [];
  for (let i = tree.length - 1; i >= 0; i--) deepestFirst.push(tree[i]);
  const deleted = deepestFirst.slice(0, api.deleteOnly ?? tree.length);
  api.places = api.places.filter((p) => !deleted.includes(p.place));
  for (const c of api.conversations) c.labelIds = c.labelIds.filter((l) => !deleted.includes(l));
  if (api.deleteOnly !== undefined) {
    api.deleteOnly = undefined;
    return problem(busy, "/api/v0/problems/mail-provider-busy", { retryAfter: 30 });
  }
  return data({ deleted });
}

// What a label write may send, as the contract has it.
const LabelWrite = z.object({
  accountId: z.number().optional(),
  name: z.string().optional(),
  parent: z.string().nullable().optional(),
});

// labelResource returns place as the API answers a label.
function labelResource(place: PlaceCount) {
  return { id: place.place, accountId: place.accountId, name: place.name, parent: place.parent };
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
  return data({
    conversations: changed,
    refused: refused.map((id) => ({ id, reason: api.refuse[id] })),
    undo,
    places: api.placesAfter,
  });
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
function searched(
  conversations: ConversationRow[],
  search: URLSearchParams,
  shift: number,
  dropped: readonly number[],
) {
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
  const found = dropped.includes(page) ? [] : results.slice(start, start + limit);
  return { results: found, total: results.length };
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
