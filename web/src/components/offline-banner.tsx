import { useSyncExternalStore, type ReactElement } from "react";
import { onlineManager } from "@tanstack/react-query";
import { WifiOffIcon } from "lucide-react";
import { Banner } from "@/components/banner";

// The banner's landmark name.
export const offlineLabel = "Offline";

// OfflineBanner says, while the browser reports no connection, that amplop
// can't reach its server, and nothing otherwise. It reads the connection
// as TanStack Query does, which pauses reads until it returns.
export function OfflineBanner(): ReactElement {
  const online = useSyncExternalStore(
    (onChange) => onlineManager.subscribe(onChange),
    () => onlineManager.isOnline(),
    () => true,
  );

  return (
    // Stays mounted, so a screen reader announces the text as it appears.
    <output className="contents">
      {!online && (
        // A landmark while it shows, so a screen reader that missed the
        // announcement finds it again (WAI-ARIA APG, "Landmark Regions").
        <section aria-label={offlineLabel} className="shrink-0">
          <Banner tone="warning" icon={WifiOffIcon}>
            You&apos;re offline. amplop shows what it already loaded and catches up when the
            connection returns.
          </Banner>
        </section>
      )}
    </output>
  );
}
