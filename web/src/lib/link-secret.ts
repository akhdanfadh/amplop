// The secret in an emailed link, kept in memory from app start until the
// page the link opens reads it. Links put the secret after #, which the
// browser never sends to a server.
//
// DESIGN: getRouter calls captureLinkSecret before anything renders or sends a
// request, so the secret leaves the address bar even when the setup gate's
// status request fails and the page never renders. Alternatives: the route's
// beforeLoad, after which the router reads the address again and the page
// loses the secret; a side effect when the route module loads, which hides
// the work in an import; and history state, which a browser may save to
// disk.

let kept: { path: string; secret: string } | undefined;

// captureLinkSecret keeps the text after # when the browser shows one of
// paths, the pages emailed links open, e.g. "/verify-email", and replaces
// the current history entry with the same address without it. Other pages,
// an empty secret, and the build's prerender, which has no window, are left
// alone.
export function captureLinkSecret(paths: readonly string[]): void {
  if (typeof window === "undefined") return;
  const { pathname, search, hash } = window.location;
  const secret = hash.slice(1);
  if (!paths.includes(pathname) || !secret) return;

  kept = { path: pathname, secret };
  window.history.replaceState(window.history.state, "", pathname + search);
}

// linkSecret returns the secret captureLinkSecret kept for path, or "" if
// none, so one page never reads another page's secret.
export function linkSecret(path: string): string {
  return kept?.path === path ? kept.secret : "";
}

// forgetLinkSecret drops the kept secret, so a later visit to its page
// without a link does not reuse it.
export function forgetLinkSecret(): void {
  kept = undefined;
}
