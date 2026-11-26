import { vi } from "vite-plus/test";
import type {
  ConnectedAccounts,
  CurrentSession,
  ProblemType,
  RegistrationAvailability,
  SystemEmailSettings,
} from "@/api/generated/model";
// Only its type: the contract's paths and methods, so a route for a path
// or method the API does not have is a type error.
import type spec from "../../../api/generated/openapi.json";

const ok = 200;
const unauthenticated = 401;
const notFound = 404;

type Paths = (typeof spec)["paths"];

// Path is an API path as the contract writes it, e.g.
// "/api/v0/connected-accounts/{id}/sync".
export type Path = `/api/v0${keyof Paths}`;

// Method is a method the contract has at path P, e.g. "POST".
export type Method<P extends Path> = P extends `/api/v0${infer K extends keyof Paths}`
  ? Uppercase<keyof Paths[K] & string>
  : never;

// VisitorOperation is each operation the contract lets a visitor call,
// those without the session cookie, as "POST /api/v0/sessions".
type VisitorOperation = {
  [K in keyof Paths]: {
    [M in keyof Paths[K]]: Paths[K][M] extends { security: { sessionCookie: unknown }[] }
      ? never
      : `${Uppercase<M & string>} /api/v0${K}`;
  }[keyof Paths[K]];
}[keyof Paths];

// The operations a signed-out server still answers. The type holds the
// list to the contract: a missing or extra operation is a type error.
const visitorOperations: Record<VisitorOperation, true> = {
  "GET /api/v0/health": true,
  "GET /api/v0/registration": true,
  "GET /api/v0/setup": true,
  "POST /api/v0/email-verifications/redeem": true,
  "POST /api/v0/password-reset/links": true,
  "POST /api/v0/password-reset/links/redeem": true,
  "POST /api/v0/registration/links": true,
  "POST /api/v0/registration/links/redeem": true,
  "POST /api/v0/sessions": true,
  "POST /api/v0/sessions/totp": true,
  "POST /api/v0/setup": true,
};

// A signed-in member's session, as the session read answers it.
export const memberSession: CurrentSession = {
  email: "ana@example.com",
  emailVerified: true,
  role: "member",
  totpRequired: false,
};

// A signed-in administrator's session, with two-factor authentication on,
// as the pages only administrators have need.
export const adminSession: CurrentSession = { ...memberSession, role: "admin" };

// System email settings that are saved and passed a test, so an
// administrator's frame shows no setup reminder.
export const testedSystemEmail: SystemEmailSettings = {
  host: "smtp.example.com",
  port: 465,
  security: "tls",
  username: "amplop@example.com",
  hasPassword: true,
  fromAddress: "amplop@example.com",
  fromName: "amplop",
  testedAt: "2026-11-12T09:38:00Z",
};

// A member's accounts list with no account, while syncing runs.
const noAccounts: ConnectedAccounts = { accounts: [], syncStopped: false };

// Registration as a visitor reads it, closed.
const closed: RegistrationAvailability = { open: false };

// Asked is one request the server got: its method, its path with the
// search, e.g. "/api/v0/conversations?place=inbox", its URL, the values of
// its route's {parameters} by name, its JSON body parsed (undefined
// without one), and its headers.
export type Asked = {
  method: string;
  path: string;
  url: URL;
  params: Record<string, string>;
  body: unknown;
  headers: Headers;
};

// Handler answers one request, asked. earlier answers it as the route
// this one replaced does, e.g. after a wait or for the requests the
// handler leaves alone. To hold the answer it awaits the promise of
// held(), which the test resolves.
export type Handler = (
  asked: Asked,
  earlier: () => Promise<Response>,
) => Response | Promise<Response>;

// Server is a fake amplop API behind fetch. on answers method at path
// with handler from then on, and once only the next such request; path is
// the contract's, and its {parameters} match one segment each. Of two
// routes that match a request, the one with fewer parameters answers, and
// of two alike the later one, so a test can replace a route. A once
// route that passes the request on to earlier still counts as used. session is
// the member's session, which the session read answers; set it to
// undefined to end it, and every operation that needs one answers a 401.
// requests holds every request in order, and unknown those no route
// knew, which got a 404 and fail the test (expectAllAnswered). fetch is
// what install puts in fetch's place.
export type Server = {
  on: <P extends Path>(method: Method<P>, path: P, handler: Handler) => void;
  once: <P extends Path>(method: Method<P>, path: P, handler: Handler) => void;
  session: CurrentSession | undefined;
  requests: Asked[];
  unknown: Asked[];
  fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
  install: () => Server;
};

type Route = { method: string; pattern: RegExp; names: string[]; handler: Handler; once: boolean };

// The servers installed since the last expectAllAnswered.
let installed: Server[] = [];

// fakeServer returns a Server that answers as a quiet amplop does, one
// with nothing to report: setup is done, the session read answers
// session, or a 401 without one, the member has no connected account and
// syncing runs, system email is saved and tested, and registration is
// closed. Those are the reads the app's frame and sign-in make on their
// own, so a test adds only its page's routes; its own route for any of
// them replaces the quiet answer.
export function fakeServer(session?: CurrentSession): Server {
  const routes: Route[] = [];
  const add = (method: string, path: string, handler: Handler, once: boolean) => {
    const names: string[] = [];
    const pattern = path.replaceAll(/\{(\w+)\}/g, (_, name: string) => {
      names.push(name);
      return "([^/]+)";
    });
    routes.push({ method, pattern: new RegExp(`^${pattern}$`), names, handler, once });
  };
  const server: Server = {
    on: (method, path, handler) => add(method, path, handler, false),
    once: (method, path, handler) => add(method, path, handler, true),
    session,
    requests: [],
    unknown: [],
    fetch: async (input, init) => {
      const request = input instanceof Request ? input : undefined;
      const href =
        typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      // The app asks by path alone, e.g. "/api/v0/places". Node has no
      // page whose address would complete it, so any origin does there.
      const url = new URL(href, globalThis.location?.href ?? "http://amplop.test");
      const text = typeof init?.body === "string" ? init.body : await request?.clone().text();
      const asked: Asked = {
        method: (init?.method ?? request?.method ?? "GET").toUpperCase(),
        path: url.pathname + url.search,
        url,
        params: {},
        body: json(text),
        headers: new Headers(init?.headers ?? request?.headers),
      };
      server.requests.push(asked);

      if (!server.session && !(`${asked.method} ${url.pathname}` in visitorOperations)) {
        // A route with parameters is never a visitor's, so the path as
        // asked is enough to tell.
        return problem(unauthenticated, "/api/v0/problems/unauthenticated");
      }
      // The routes that match, the one to answer first: fewest
      // parameters, then latest.
      const matching = routes
        .map((route, index) => ({ route, index }))
        .filter(({ route }) => route.method === asked.method && route.pattern.test(url.pathname))
        // oxlint-disable-next-line unicorn/no-array-sort -- a copy; tsconfig's lib has no toSorted.
        .sort((a, b) => a.route.names.length - b.route.names.length || b.index - a.index)
        .map(({ route }) => route);
      const answer = async (nth: number): Promise<Response> => {
        if (nth >= matching.length) {
          server.unknown.push(asked);
          return problem(notFound, "/api/v0/problems/not-found");
        }
        const route = matching[nth];
        if (route.once) routes.splice(routes.indexOf(route), 1);
        const values = route.pattern.exec(url.pathname) ?? [];
        asked.params = Object.fromEntries(
          route.names.map((name, i) => [name, decodeURIComponent(values[i + 1])]),
        );
        return route.handler(asked, () => answer(nth + 1));
      };
      return answer(0);
    },
    install: () => {
      vi.stubGlobal("fetch", vi.fn(server.fetch));
      installed.push(server);
      return server;
    },
  };
  // The root asks whether amplop still needs its first administrator.
  server.on("GET", "/api/v0/setup", () => data({ status: "done" }));
  server.on("GET", "/api/v0/sessions/current", () => data(server.session));
  // The signed-in frame's sync banner.
  server.on("GET", "/api/v0/connected-accounts", () => data(noAccounts));
  // An administrator's setup reminder.
  server.on("GET", "/api/v0/system-email", () => data({ settings: testedSystemEmail }));
  // Sign-in asks whether to offer registration.
  server.on("GET", "/api/v0/registration", () => data(closed));
  return server;
}

// expectAllAnswered throws an Error naming each request that no route of
// a server installed since its last call knew, e.g. "Requests without an
// answer: GET /api/v0/places", and forgets those servers. The setup of
// every test calls it after each test, so a page's read that the fake
// lacks fails its test; a test about a failed read gives the read a
// route that answers the failure.
export function expectAllAnswered(): void {
  const unknown = installed.flatMap((server) =>
    server.unknown.map((asked) => `${asked.method} ${asked.path}`),
  );
  installed = [];
  if (unknown.length > 0) throw new Error(`Requests without an answer: ${unknown.join(", ")}`);
}

// json returns text parsed as JSON, or undefined when it is none.
function json(text: string | undefined): unknown {
  try {
    return text === undefined ? undefined : JSON.parse(text);
  } catch {
    return undefined;
  }
}

// data returns the API's answer holding value, { data: value }, with
// status, 200 when left out.
export function data(value: unknown, status = ok): Response {
  return Response.json({ data: value }, { status });
}

// problem returns a Problem Details answer with status and type, as the
// API sends its errors. more overrides or adds members, e.g. detail or a
// 422's errors; retryAfter, in whole seconds, is sent as Retry-After.
export function problem(
  status: number,
  type: ProblemType | "about:blank" = "about:blank",
  { retryAfter, ...more }: { retryAfter?: number } & Record<string, unknown> = {},
): Response {
  return Response.json(
    { type, status, title: "Failed", detail: "It failed.", ...more },
    {
      status,
      headers: {
        "Content-Type": "application/problem+json",
        ...(retryAfter !== undefined && { "Retry-After": String(retryAfter) }),
      },
    },
  );
}

// held returns a promise for a handler to await, which holds its answer
// until the test calls resolve.
//
// NOTE: this stands in for Promise.withResolvers, a type error under
// tsconfig's lib, ES2022. The lib also types the app's code, which must
// not call what older browsers lack, so it stays.
export function held(): { promise: Promise<void>; resolve: () => void } {
  const settle: { resolve?: () => void } = {};
  const promise = new Promise<void>((resolve) => {
    settle.resolve = resolve;
  });
  return { promise, resolve: () => settle.resolve?.() };
}

// lose loses the answer, for a handler to call after it acted: fetch
// rejects as it does when the connection drops.
export function lose(): never {
  throw new TypeError("Failed to fetch");
}
