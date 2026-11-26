import type { ReactElement } from "react";
import { Spinner } from "@/components/ui/spinner";
import { loadingMessage } from "./text";

// MessageLoading says a message's content is loading, with a spinner,
// after 300 ms (DESIGN.md "States").
export function MessageLoading(): ReactElement {
  return (
    // Delayed, so content from amplop's own store, which usually comes
    // sooner, appears without a flash.
    <output className="flex pending-delay items-center gap-2 text-muted-foreground">
      <Spinner />
      {loadingMessage}
    </output>
  );
}
