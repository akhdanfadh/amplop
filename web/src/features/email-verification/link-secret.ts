// The secret in an emailed verification link, kept in memory from app start
// until the /verify-email page opens.
//
// NOTE: getRouter calls captureLinkSecret before anything renders or sends a
// request, so the secret leaves the address bar even when the setup gate's
// status request fails and the page never renders. Alternatives: the route's
// beforeLoad, after which the router reads the address again and the page
// loses the secret; a side effect when the route module loads, which hides
// the work in an import; and history state, which a browser may save to
// disk.

const verifyEmailPath = "/verify-email";

let kept: string | undefined;

// captureLinkSecret keeps the text after # when the browser shows
// /verify-email, and replaces the current history entry with the same
// address without it. Other pages, an empty secret, and the build's
// prerender, which has no window, are left alone.
export function captureLinkSecret(): void {
  if (typeof window === "undefined") return;
  const { pathname, search, hash } = window.location;
  const secret = hash.slice(1);
  if (pathname !== verifyEmailPath || !secret) return;

  kept = secret;
  window.history.replaceState(window.history.state, "", pathname + search);
}

// linkSecret returns the secret captureLinkSecret kept, or "" if none.
export function linkSecret(): string {
  return kept ?? "";
}

// forgetLinkSecret drops the kept secret, so a later visit to /verify-email
// without a link does not reuse it.
export function forgetLinkSecret(): void {
  kept = undefined;
}
