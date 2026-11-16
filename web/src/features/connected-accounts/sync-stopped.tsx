import { useEffect, useState, type ReactElement } from "react";
import { MailXIcon } from "lucide-react";
import { useGetCurrentSession, useListConnectedAccounts } from "@/api/generated/inbok";
import { Banner } from "@/components/banner";
import { loadedFreshMs } from "@/api/cached-read";
import { idleRefresh } from "./sync";
import { syncStoppedText } from "./text";

// SyncStopped says, on every signed-in page, that inbok has stopped getting
// new mail for every account, while the accounts list says syncing stopped
// on this inbok, and nothing otherwise: before its first read answers, and
// after a failed read, it keeps the last answer. It reads the list again
// every minute, as Connected accounts does when nothing moves, so it
// follows a stop or a recovery on any page. Only an administrator can fix
// it, so the words differ by role.
export function SyncStopped(): ReactElement {
  // The frame's own session read, so this adds no request.
  const session = useGetCurrentSession({
    query: { refetchOnMount: false, refetchOnReconnect: false },
  }).data?.data;
  // A list a loader just read, e.g. mail's or this page's, counts as fresh.
  const list = useListConnectedAccounts({
    query: { staleTime: loadedFreshMs, refetchInterval: idleRefresh },
  });
  const stopped = list.data?.data.syncStopped ?? false;
  // Whether the live region exists yet. A screen reader announces a live
  // region's change, not what it already holds when it appears (MDN, "ARIA
  // live regions"), and a loader often has the list before the frame
  // draws, so the banner fills a frame after the region does.
  const [listening, setListening] = useState(false);

  useEffect(() => {
    const frame = requestAnimationFrame(() => setListening(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  return (
    <output className="contents">
      {listening && stopped && (
        // A landmark while it shows, so a screen reader that missed the
        // announcement finds it again (WAI-ARIA APG, "Landmark Regions").
        <section aria-label={syncStoppedText.label} className="shrink-0">
          <Banner tone="warning" icon={MailXIcon}>
            {session?.role === "admin" ? syncStoppedText.admin : syncStoppedText.member}
          </Banner>
        </section>
      )}
    </output>
  );
}
