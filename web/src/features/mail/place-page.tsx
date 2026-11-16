import {
  useEffect,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeftIcon, SearchIcon } from "lucide-react";
import { AccountMark, accountName } from "@/components/account-mark";
import { useTopBarSlot } from "@/components/frame";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { useFocusOnOpen } from "@/hooks/use-focus-on-open";
import { useLayout } from "@/hooks/use-layout";
import { CheckMail } from "./check-mail";
import { listKey, useRowFocusRequest } from "./focus-request";
import { ConversationList } from "./list";
import { MailPanes } from "./panes";
import { UndoBar } from "./undo";
import { isViewPlace, searchPlace, type Place } from "./places";
import { labelPath, usePlaceCounts } from "./place-counts";
import type { FilterChange, SearchFilters } from "./search";
import { SearchBox, useSearchBox } from "./search-box";
import { SearchResults } from "./search-results";
import { closeSearch, openSearch, placeName, searchPlaceholder } from "./text";

// PlaceAccount is the account a place's list is filtered to.
export type PlaceAccount = { id: number; color: number; email: string; name: string | null };

// PlacePage is a place's mail: its conversation list beside reader, the
// reading pane, with the search box above the list. name is the place's
// name, its heading, and ownName its own name, a label's without its
// parents (name by default); account, when given, the one account the
// list shows; openId the conversation reader holds. pane says what was
// opened: the list, with nothing in the reading pane, or the reader, e.g.
// a conversation or Page not found. search, on /mail/search, is the
// search whose results take the list's place.
//
// DESIGN: one page for a place and its search, so the box, typed in on a
// place, is still the same box, focused, once its words open the results.
export function PlacePage({
  place,
  name,
  ownName = name,
  account,
  openId,
  pane,
  reader,
  search,
}: {
  place: Place;
  name: string;
  ownName?: string;
  account: PlaceAccount | undefined;
  openId: string | undefined;
  pane: "list" | "reader";
  reader: ReactNode;
  search?: SearchFilters;
}): ReactElement {
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const boxRef = useRef<HTMLInputElement>(null);
  const openSearchRef = useRef<HTMLButtonElement>(null);
  // Whether the search box has focus.
  const [boxFocused, setBoxFocused] = useState(false);
  // Try again on a list whose first page failed starts it anew.
  const [attempt, setAttempt] = useState(0);
  const topBar = useTopBarSlot();
  const slot = topBar?.box ?? null;
  const inBar = topBar?.shown ?? false;
  // In a top bar the header waits for its box, which the frame has after
  // its first draw, so the heading is drawn once, in its place, and
  // focused there.
  const placed = !inBar || slot !== null;
  const searching = search !== undefined;
  // Where the top bar has the box, it shows once its button is pressed,
  // and for a search's results.
  const [barOpen, setBarOpen] = useState(false);
  const barBox = searching || barOpen;
  // Focus goes back to the top bar's search button once the box closes,
  // after the place's heading takes it as the place opens.
  const refocus = useRef(false);
  useEffect(() => {
    if (!refocus.current || barBox) return undefined;
    refocus.current = false;
    const frame = requestAnimationFrame(() => openSearchRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [barBox]);

  // Focus goes back to the box once leaving with its clear button, which
  // took it, has settled: after the place's heading takes it as the
  // place opens, or at once on a place's own page.
  const refocusBox = useRef(false);
  useEffect(() => {
    if (!refocusBox.current || searching) return undefined;
    refocusBox.current = false;
    const frame = requestAnimationFrame(() => boxRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [searching]);

  const box = useSearchBox({
    place,
    account: account?.id,
    search,
    onLeft: () => {
      if (inBar) {
        setBarOpen(false);
        refocus.current = true;
      } else if (searching) {
        refocusBox.current = true;
      } else {
        boxRef.current?.focus();
      }
    },
  });
  const counts = usePlaceCounts();
  const scope =
    search?.place?.length === 1
      ? isViewPlace(search.place[0])
        ? placeName[search.place[0]]
        : labelPath(counts, search.place[0]).at(-1)
      : !searching && place !== "inbox" && place !== "all"
        ? ownName
        : undefined;

  // A list asked to focus a row, as an action sends the member back to it,
  // takes focus itself.
  const focusKey = listKey(place, account?.id);
  const rowAsked = useRowFocusRequest(focusKey) !== undefined;
  // Each page focuses its heading when it opens (DESIGN.md "Focus"), so the
  // keyboard starts in the place, not back in the sidebar; what the reading
  // pane shows takes focus itself, and so does the box while the member
  // types in it, as their words open the results.
  useFocusOnOpen(
    headingRef,
    pane === "list" && placed ? `${searching ? searchPlace : place} ${account?.id ?? ""}` : null,
    { unless: rowAsked || boxFocused },
  );
  // On a phone the open conversation has its own bar, so the place's
  // header leaves the top bar to the wordmark.
  const phone = useLayout() === "phone";
  const hidden = phone && pane === "reader";

  // In the top bar the box takes the bar's buttons' height, 32 px (44 px
  // on touch), so the bar keeps its height as it turns into the box.
  const searchBox = (dense: boolean) => (
    <SearchBox
      className={dense ? "h-8" : undefined}
      inputRef={boxRef}
      value={box.text}
      placeholder={searchPlaceholder(scope)}
      onChange={box.onChange}
      onEnter={box.onEnter}
      onLeave={box.onLeave}
      onFocusChange={setBoxFocused}
    />
  );
  // The top bar: the box once opened, with a way back; else the place's
  // header with the button that opens the box.
  const barContent = barBox ? (
    <BarSearch
      box={searchBox(true)}
      boxRef={boxRef}
      onClose={() => {
        if (searching) box.onLeave();
        else {
          setBarOpen(false);
          refocus.current = true;
        }
      }}
    />
  ) : (
    <PlaceHeader
      name={name}
      ownName={ownName}
      account={account}
      headingRef={headingRef}
      inTopBar
      searchButton={
        <TooltipTrigger>
          <Button
            ref={openSearchRef}
            variant="ghost"
            size="icon-sm"
            aria-label={openSearch}
            onPress={() => setBarOpen(true)}
          >
            <SearchIcon aria-hidden="true" />
          </Button>
          <Tooltip>{openSearch}</Tooltip>
        </TooltipTrigger>
      }
    />
  );

  const onFilters = (change: FilterChange) => {
    const { account: nextAccount, ...rest } = change;
    void navigate({
      to: "/mail/$place",
      params: { place: searchPlace },
      search: {
        ...search,
        ...rest,
        account: "account" in change ? nextAccount : account?.id,
      },
      replace: true,
    });
  };

  // The Undo bar docks at the bottom of the list, under the row the action
  // took away, and on a phone, which shows one pane at a time, under
  // whichever shows, as an action can keep the reader open there; it covers
  // neither (DESIGN.md "Feedback").
  const undoBar = <UndoBar fallbackFocus={headingRef} />;
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <MailPanes
        pane={pane}
        list={
          <main className="flex h-full flex-col" aria-labelledby="place-heading">
            {slot ? !hidden && createPortal(barContent, slot) : null}
            {!inBar && <div className="px-4 pt-3">{searchBox(false)}</div>}
            {search ? (
              <>
                {/* The results have no heading on screen: the box and the
                    sentence under it say what they are. */}
                <h1 ref={headingRef} id="place-heading" tabIndex={-1} className="sr-only">
                  {name}
                </h1>
                <SearchResults
                  filters={search}
                  account={account?.id}
                  onChange={onFilters}
                  openId={openId}
                  focusKey={focusKey}
                  heading={headingRef}
                />
              </>
            ) : (
              <>
                {!inBar && (
                  <PlaceHeader
                    name={name}
                    ownName={ownName}
                    account={account}
                    headingRef={headingRef}
                    inTopBar={false}
                  />
                )}
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
              </>
            )}
            {!phone && undoBar}
          </main>
        }
        reader={reader}
      />
      {phone && undoBar}
    </div>
  );
}

// BarSearch is the top bar turned into the search box: a way back, then
// box, which takes focus as it opens.
function BarSearch({
  box,
  boxRef,
  onClose,
}: {
  box: ReactNode;
  boxRef: RefObject<HTMLInputElement | null>;
  onClose: () => void;
}): ReactElement {
  useEffect(() => {
    if (document.activeElement !== boxRef.current) boxRef.current?.focus();
  }, [boxRef]);
  return (
    <>
      <TooltipTrigger>
        <Button variant="ghost" size="icon-sm" aria-label={closeSearch} onPress={onClose}>
          <ArrowLeftIcon aria-hidden="true" />
        </Button>
        <Tooltip>{closeSearch}</Tooltip>
      </TooltipTrigger>
      <div className="min-w-0 flex-1">{box}</div>
    </>
  );
}

// PlaceHeader is a place's heading, name, with the account the list is
// filtered to and Check for new mail. inTopBar puts it in the top bar
// (DESIGN.md "Phones"), with ownName, a label's own name, in place of its
// path, and searchButton after Check for new mail; else it heads the list.
function PlaceHeader({
  name,
  ownName,
  account,
  headingRef,
  inTopBar,
  searchButton,
}: {
  name: string;
  ownName: string;
  account: PlaceAccount | undefined;
  headingRef: RefObject<HTMLHeadingElement | null>;
  inTopBar: boolean;
  searchButton?: ReactNode;
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
      <span className="ml-auto flex shrink-0 items-center gap-1">
        <CheckMail accountId={account?.id} />
        {searchButton}
      </span>
    </>
  );
  if (inTopBar) return content;
  return <header className="flex items-center gap-3 border-b py-2 pr-2 pl-4">{content}</header>;
}
