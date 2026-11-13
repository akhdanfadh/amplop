import { useRef, useState, type ReactElement, type ReactNode } from "react";
import { AccountMark, accountName } from "@/components/account-mark";
import { useFocusOnOpen } from "@/hooks/use-focus-on-open";
import { ConversationList } from "./list";
import { MailPanes } from "./panes";
import type { Place } from "./places";

// PlaceAccount is the account a place's list is filtered to.
export type PlaceAccount = { id: number; color: number; email: string; name: string | null };

// PlacePage is a place's mail: its conversation list beside reader, the
// reading pane. name is the place's name, its heading; account, when
// given, the one account the list shows; openId the conversation reader
// holds. pane says what was opened: the list, with nothing in the reading
// pane, or the reader, e.g. a conversation or Page not found.
export function PlacePage({
  place,
  name,
  account,
  openId,
  pane,
  reader,
}: {
  place: Place;
  name: string;
  account: PlaceAccount | undefined;
  openId: string | undefined;
  pane: "list" | "reader";
  reader: ReactNode;
}): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  // Try again on a list whose first page failed starts it anew.
  const [attempt, setAttempt] = useState(0);
  // Each page focuses its heading when it opens (DESIGN.md "Focus"), so the
  // keyboard starts in the place, not back in the sidebar; what the reading
  // pane shows takes focus itself.
  useFocusOnOpen(headingRef, pane === "list" ? `${place} ${account?.id ?? ""}` : null);

  return (
    <MailPanes
      pane={pane}
      list={
        <main className="flex h-full flex-col" aria-labelledby="place-heading">
          <header className="flex items-baseline gap-3 border-b px-4 py-3">
            <h1
              ref={headingRef}
              id="place-heading"
              tabIndex={-1}
              className="truncate text-lg font-semibold"
            >
              {name}
            </h1>
            {account && (
              <AccountMark
                color={account.color}
                name={accountName(account)}
                className="text-sm text-muted-foreground"
              />
            )}
          </header>
          <ConversationList
            // A new place, account, or attempt is a new list: its own
            // scroll position, "N new" count, and first load.
            key={`${place} ${account?.id ?? ""} ${attempt}`}
            place={place}
            placeName={name}
            account={account?.id}
            openId={openId}
            onRetry={() => setAttempt((n) => n + 1)}
          />
        </main>
      }
      reader={reader}
    />
  );
}
