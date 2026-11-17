import { vi } from "vite-plus/test";
import type { CurrentSession, ProblemType } from "@/api/generated/model";
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
  "POST /api/v0/probe/events": true,
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
// handler leaves alone. To hold the answer it awaits a promise the test
// settles, e.g. Promise.withResolvers()'s.
export type Handler = (
  asked: Asked,
  earlier: () => Promise<Response>,
) => Response | Promise<Response>;

// Server is a fake inbok API behind fetch. on answers method at path
// with handler from then on, and once only the next such request; path is
// the contract's, and its {parameters} match one segment each. Of two
// routes that match a request, the one with fewer parameters answers, and
// of two alike the later one, so a test can replace a route. A once
// route that passes the request on to earlier still counts as used. session is
// the member's session, which the session read answers; set it to
// undefined to end it, and every operation that needs one answers a 401.
// requests holds every request in order, and unknown those no route
// knew, which got a 404. fetch is what install puts in fetch's place.
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

// fakeServer returns a Server that answers the session read with session,
// or a 401 without one, and says that setup is done.
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
      const url = new URL(href, globalThis.location?.href);
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
      return server;
    },
  };
  // The root asks whether inbok still needs its first administrator.
  server.on("GET", "/api/v0/setup", () => data({ status: "done" }));
  server.on("GET", "/api/v0/sessions/current", () => data(server.session));
  return server;
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

// lose loses the answer, for a handler to call after it acted: fetch
// rejects as it does when the connection drops.
export function lose(): never {
  throw new TypeError("Failed to fetch");
}
