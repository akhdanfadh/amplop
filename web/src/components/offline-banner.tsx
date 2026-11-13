import { useSyncExternalStore, type ReactElement } from "react";
import { onlineManager } from "@tanstack/react-query";
import { WifiOffIcon } from "lucide-react";

// OfflineBanner says, while the browser reports no connection, that inbok
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
        <p className="flex items-center gap-2 bg-warning/8 px-4 py-2 text-sm text-warning dark:bg-warning/12">
          <WifiOffIcon aria-hidden="true" className="size-4 shrink-0" />
          You&apos;re offline. inbok shows what it already loaded and catches up when the connection
          returns.
        </p>
      )}
    </output>
  );
}
