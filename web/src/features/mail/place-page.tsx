import { useRef, useState, type ReactElement, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { AccountMark, accountName } from "@/components/account-mark";
import { useTopBarSlot } from "@/components/frame";
import { useFocusOnOpen } from "@/hooks/use-focus-on-open";
import { useLayout } from "@/hooks/use-layout";
import { CheckMail } from "./check-mail";
import { listKey, useRowFocusRequest } from "./focus-request";
import { ConversationList } from "./list";
import { MailPanes } from "./panes";
import type { Place } from "./places";

// PlaceAccount is the account a place's list is filtered to.
export type PlaceAccount = { id: number; color: number; email: string; name: string | null };

// PlacePage is a place's mail: its conversation list beside reader, the
// reading pane. name is the place's name, its heading, and ownName its own
// name, a label's without its parents (name by default); account, when
// given, the one account the list shows; openId the conversation reader
// holds. pane says what was opened: the list, with nothing in the reading
// pane, or the reader, e.g. a conversation or Page not found.
export function PlacePage({
  place,
  name,
  ownName = name,
  account,
  openId,
  pane,
  reader,
}: {
  place: Place;
  name: string;
  ownName?: string;
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
  const topBar = useTopBarSlot();
  const slot = topBar?.box ?? null;
  const inBar = topBar?.shown ?? false;
  // In a top bar the header waits for its box, which the frame has after
  // its first draw, so the heading is drawn once, in its place, and
  // focused there.
  const placed = !inBar || slot !== null;
  // A list asked to focus a row, as an action sends the member back to it,
  // takes focus itself.
  const rowAsked = useRowFocusRequest(listKey(place, account?.id)) !== undefined;
  useFocusOnOpen(headingRef, pane === "list" && placed ? `${place} ${account?.id ?? ""}` : null, {
    unless: rowAsked,
  });
  // On a phone the open conversation has its own bar, so the place's
  // header leaves the top bar to the wordmark.
  const hidden = useLayout() === "phone" && pane === "reader";
  const header = (
    <PlaceHeader
      name={name}
      ownName={ownName}
      account={account}
      headingRef={headingRef}
      inTopBar={slot !== null}
    />
  );

  return (
    <MailPanes
      pane={pane}
      list={
        <main className="flex h-full flex-col" aria-labelledby="place-heading">
          {slot ? !hidden && createPortal(header, slot) : !inBar && header}
          <ConversationList
            // A new place, account, or attempt is a new list: its own
            // scroll position, "N new" count, and first load.
            key={`${place} ${account?.id ?? ""} ${attempt}`}
            place={place}
            placeName={ownName}
            account={account?.id}
            openId={openId}
            heading={headingRef}
            onRetry={() => setAttempt((n) => n + 1)}
          />
        </main>
      }
      reader={reader}
    />
  );
}

// PlaceHeader is a place's heading, name, with the account the list is
// filtered to and Check for new mail. inTopBar puts it in the top bar (DESIGN.md "Phones"), with
// ownName, a label's own name, in place of its path; else it heads the
// list.
function PlaceHeader({
  name,
  ownName,
  account,
  headingRef,
  inTopBar,
}: {
  name: string;
  ownName: string;
  account: PlaceAccount | undefined;
  headingRef: RefObject<HTMLHeadingElement | null>;
  inTopBar: boolean;
}): ReactElement {
  const content = (
    <>
      {/* The words share one baseline; the row is centred beside the
          buttons. */}
      <div className="flex min-w-0 items-baseline gap-2">
        <h1
          ref={headingRef}
          id="place-heading"
          tabIndex={-1}
          className="truncate text-lg font-semibold"
        >
          {inTopBar && ownName !== name ? (
            // The top bar has room for the own name, the part that tells a
            // label from its parents; screen readers still hear the path.
            <>
              <span className="sr-only">{name}</span>
              <span aria-hidden="true">{ownName}</span>
            </>
          ) : (
            name
          )}
        </h1>
        {account && (
          <AccountMark
            color={account.color}
            name={accountName(account)}
            className="text-sm text-muted-foreground"
          />
        )}
      </div>
      <span className="ml-auto shrink-0">
        <CheckMail accountId={account?.id} />
      </span>
    </>
  );
  if (inTopBar) return content;
  return <header className="flex items-center gap-3 border-b py-2 pr-2 pl-4">{content}</header>;
}
