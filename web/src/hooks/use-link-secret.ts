import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { forgetLinkSecret, linkSecret } from "@/lib/link-secret";

// useLinkSecret returns the secret of the emailed link that opened path, or
// "" when the page opened without one. Call it only in path's page, and
// pass path to captureLinkSecret too.
export function useLinkSecret(path: string): string {
  const navigate = useNavigate();
  const hash = useLocation({ select: (location) => location.hash });
  // getRouter took the opening link's secret out of the address at start; a
  // link pasted into this tab later changes only the hash, which wins.
  const [secret, setSecret] = useState(() => hash || linkSecret(path));
  if (hash && hash !== secret) setSecret(hash);

  // A later visit without a link must not reuse this one.
  useEffect(() => {
    forgetLinkSecret();
  }, []);

  // Replacing the entry keeps a pasted link's secret out of the address bar,
  // Back, and copied URLs.
  useEffect(() => {
    if (hash) void navigate({ to: path, replace: true });
  }, [hash, navigate, path]);

  return secret;
}
