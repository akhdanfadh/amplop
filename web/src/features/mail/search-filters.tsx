import { useEffect, useId, type ReactElement } from "react";
import { Button as AriaButton, type Selection } from "react-aria-components";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import { searchConversationsParamsFromMax } from "@/api/generated/model/searchConversationsParams.zod";
import { AccountMark, accountName } from "@/components/account-mark";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Popover, PopoverTrigger } from "@/components/ui/popover";
import { RecipientInput } from "@/components/recipient-input";
import { isAddress } from "@/features/outgoing-messages/compose-schema";
import { dayDate } from "@/lib/datetime";
import { useLabels, type Label } from "./labels";
import { isViewPlace, viewPlaces, type Place } from "./places";
import { mostPlaces, type FilterChange, type SearchFilters } from "./search";
import { DateChooser } from "./search-dates";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { useOverflow } from "@/hooks/use-overflow";
import {
  accountMenuName,
  allAccountsName,
  anyAttachment,
  anyTime,
  attachmentMenuName,
  clearName,
  dateMenuName,
  datePresetPhrases,
  doneName,
  everyAccount,
  fromHelp,
  fromName,
  fromPhrase,
  placeName,
  placesLimit,
  placesMenuName,
  placesPhrase,
  rangePhrase,
  searchingWord,
  inWord,
  fromWord,
  sendersName,
  viewsGroupName,
  withAttachment,
} from "./text";

// placeLabel returns the name of place, a view or one of labels: a label's
// own name, as the top bar names one, its path being in the menu.
function placeLabel(place: Place, labels: readonly Label[]): string {
  if (isViewPlace(place)) return placeName[place];
  return labels.find((label) => label.place === place)?.name ?? place;
}

// The sentence's words that open its menus: underlined dots, solid on
// hover, in the text colour, so they read as the parts to change. Each
// keeps to one line, cut short with an ellipsis: a long address or label
// would otherwise stand as a block of its own and break the sentence.
const phrase =
  "inline-block max-w-full truncate align-bottom cursor-default rounded-sm font-medium text-foreground underline decoration-muted-foreground decoration-dotted underline-offset-4 transition-colors outline-none data-focus-visible:focus-ring data-hovered:decoration-foreground data-hovered:decoration-solid data-pressed:decoration-solid pointer-coarse:py-2.5";

// Phrase is one of the sentence's menu words, text; cut short, it shows
// whole in a tooltip on hover and focus (DESIGN.md "Sidebar" does the same
// for names).
function Phrase({ children: text }: { children: string }): ReactElement {
  const [ref, cut] = useOverflow(text);
  return (
    <TooltipTrigger isDisabled={!cut}>
      <AriaButton ref={ref} className={phrase}>
        {text}
      </AriaButton>
      <Tooltip>{text}</Tooltip>
    </TooltipTrigger>
  );
}

// SearchSentence is a search's filters as one sentence, each part a menu
// that changes it, through onChange: "Searching all mail in every
// account, from anyone, any time, with or without attachments."
export function SearchSentence({
  filters,
  account,
  onChange,
}: {
  filters: SearchFilters;
  account: number | undefined;
  onChange: (change: FilterChange) => void;
}): ReactElement {
  const labels = useLabels();
  const accounts = useListConnectedAccounts().data?.data.accounts ?? [];
  const chosen = accounts.find((a) => a.id === account);
  const places = filters.place ?? [];
  const from = filters.from ?? [];
  const date = filters.date
    ? datePresetPhrases[filters.date]
    : filters.since || filters.before
      ? rangePhrase(dayWords(filters.since), dayWords(filters.before))
      : anyTime;

  return (
    <p className="text-sm leading-7 text-muted-foreground">
      {searchingWord}{" "}
      <DropdownMenuTrigger>
        <Phrase>{placesPhrase(places.map((place) => placeLabel(place, labels)))}</Phrase>
        <PlacesMenu
          places={places}
          labels={account === undefined ? labels : labels.filter((l) => l.accountId === account)}
          accounts={accounts}
          onChange={(place) => onChange({ place })}
        />
      </DropdownMenuTrigger>{" "}
      {inWord}{" "}
      <DropdownMenuTrigger>
        <Phrase>{chosen ? accountName(chosen) : everyAccount}</Phrase>
        <DropdownMenu
          aria-label={accountMenuName}
          className="w-64"
          selectionMode="single"
          disallowEmptySelection
          selectedKeys={new Set([account === undefined ? "all" : String(account)])}
          onSelectionChange={(selection) => {
            const [key] = keys(selection);
            if (key === undefined) return;
            const next = key === "all" ? undefined : Number(key);
            // A label is one account's: another's would match nothing.
            const kept = places.filter(
              (place) =>
                next === undefined ||
                isViewPlace(place) ||
                labels.find((l) => l.place === place)?.accountId === next,
            );
            onChange({ account: next, place: kept.length > 0 ? kept : undefined });
          }}
        >
          <DropdownMenuItem id="all">{allAccountsName}</DropdownMenuItem>
          {accounts.map((a) => (
            <DropdownMenuItem key={a.id} id={String(a.id)} textValue={accountName(a)}>
              <AccountMark color={a.color} name={accountName(a)} />
            </DropdownMenuItem>
          ))}
        </DropdownMenu>
      </DropdownMenuTrigger>
      , {fromWord}{" "}
      <PopoverTrigger>
        <Phrase>{fromPhrase(from)}</Phrase>
        <Popover aria-label={fromName} placement="bottom start" className="w-80">
          {({ close }) => (
            <FromEditor
              from={from}
              onChange={(next) => onChange({ from: next.length > 0 ? next : undefined })}
              close={close}
            />
          )}
        </Popover>
      </PopoverTrigger>
      ,{" "}
      <PopoverTrigger>
        <Phrase>{date}</Phrase>
        <Popover aria-label={dateMenuName} placement="bottom start" className="w-auto">
          {({ close }) => (
            <DateChooser
              filters={filters}
              onChange={(change) => {
                onChange(change);
                close();
              }}
            />
          )}
        </Popover>
      </PopoverTrigger>
      ,{" "}
      <DropdownMenuTrigger>
        <Phrase>{filters.has === "attachment" ? withAttachment : anyAttachment}</Phrase>
        <DropdownMenu
          aria-label={attachmentMenuName}
          className="w-64"
          selectionMode="single"
          disallowEmptySelection
          selectedKeys={new Set([filters.has ?? "any"])}
          onSelectionChange={(selection) =>
            onChange({ has: keys(selection)[0] === "attachment" ? "attachment" : undefined })
          }
        >
          <DropdownMenuItem id="any">{capitalized(anyAttachment)}</DropdownMenuItem>
          <DropdownMenuItem id="attachment">{capitalized(withAttachment)}</DropdownMenuItem>
        </DropdownMenu>
      </DropdownMenuTrigger>
      .
    </p>
  );
}

// PlacesMenu picks any number of places, views then each account's
// labels; none searches all mail. It stays open while the member picks.
function PlacesMenu({
  places,
  labels,
  accounts,
  onChange,
}: {
  places: readonly Place[];
  labels: readonly Label[];
  accounts: readonly { id: number; name: string | null; email: string }[];
  onChange: (places: Place[] | undefined) => void;
}): ReactElement {
  // Each group keeps its own picks; together they are the places, in the
  // menu's order, so the sentence names them as the sidebar lists them.
  const order: string[] = [...viewPlaces, ...labels.map((l) => l.place)];
  const setGroup = (own: readonly string[], picked: string[]) => {
    const next = new Set([...places.filter((place) => !own.includes(place)), ...picked]);
    const known = order.filter((place) => next.has(place));
    const rest = [...next].filter((place) => !order.includes(place));
    const all = [...known, ...rest];
    onChange(all.length > 0 ? all : undefined);
  };
  const withLabels = accounts.filter((a) => labels.some((l) => l.accountId === a.id));
  // At the API's limit, only a picked place can change: unticked.
  const full = places.length >= mostPlaces;
  // Each group keeps its own selection, so each item takes the limit:
  // React Aria's menu-wide disabled keys don't reach a group's items.
  const blocked = (place: string) => full && !places.includes(place);
  return (
    <DropdownMenu aria-label={placesMenuName} className="max-h-96 w-64" shouldCloseOnSelect={false}>
      <DropdownMenuGroup
        aria-label={viewsGroupName}
        selectionMode="multiple"
        selectedKeys={new Set(places.filter(isViewPlace))}
        onSelectionChange={(selection) => setGroup(viewPlaces, keys(selection))}
      >
        {full && <DropdownMenuLabel>{placesLimit(mostPlaces)}</DropdownMenuLabel>}
        {viewPlaces.map((view) => (
          <DropdownMenuItem key={view} id={view} isDisabled={blocked(view)}>
            {placeName[view]}
          </DropdownMenuItem>
        ))}
      </DropdownMenuGroup>
      {withLabels.map((a) => {
        const own = labels.filter((l) => l.accountId === a.id).map((l) => l.place);
        return (
          <DropdownMenuGroup
            key={a.id}
            aria-label={accountName(a)}
            selectionMode="multiple"
            selectedKeys={new Set(places.filter((place) => own.includes(place)))}
            onSelectionChange={(selection) => setGroup(own, keys(selection))}
          >
            <DropdownMenuSeparator />
            <DropdownMenuLabel>{accountName(a)}</DropdownMenuLabel>
            {labels
              .filter((l) => l.accountId === a.id)
              .map((l) => (
                <DropdownMenuItem key={l.place} id={l.place} isDisabled={blocked(l.place)}>
                  {l.path}
                </DropdownMenuItem>
              ))}
          </DropdownMenuGroup>
        );
      })}
    </DropdownMenu>
  );
}

// FromEditor takes senders' addresses as chips, as compose takes
// recipients; any of them matches.
function FromEditor({
  from,
  onChange,
  close,
}: {
  from: readonly string[];
  onChange: (from: string[]) => void;
  close: () => void;
}): ReactElement {
  const id = useId();
  // The popover opens to type in.
  useEffect(() => {
    const frame = requestAnimationFrame(() => document.getElementById(`${id}-field`)?.focus());
    return () => cancelAnimationFrame(frame);
  }, [id]);
  return (
    <div className="flex flex-col gap-2 p-2">
      <label htmlFor={`${id}-field`} className="text-sm font-medium">
        {fromName}
      </label>
      <p id={`${id}-help`} className="text-xs text-muted-foreground">
        {fromHelp(searchConversationsParamsFromMax)}
      </p>
      <RecipientInput
        id={`${id}-field`}
        label={sendersName}
        // A sender is an address, as the compose form's recipients are.
        isAddress={isAddress}
        value={[...from]}
        onChange={(next) => onChange(next.slice(0, searchConversationsParamsFromMax))}
        describedBy={`${id}-help`}
        // The field's ring while its input has focus, as a single input
        // shows it (DESIGN.md "Focus"); its chips keep their own.
        className="min-h-9 rounded-lg border border-input bg-card px-1 has-[input:focus-visible]:focus-ring dark:bg-input/10"
      />
      <div className="flex justify-end gap-2">
        {from.length > 0 && (
          <Button variant="ghost" size="sm" onPress={() => onChange([])}>
            {clearName}
          </Button>
        )}
        <Button size="sm" onPress={close}>
          {doneName}
        </Button>
      </div>
    </div>
  );
}

// keys returns a menu's picked keys as text.
function keys(selection: Selection): string[] {
  return selection === "all" ? [] : [...selection].map(String);
}

// capitalized returns words as a menu item starts them.
function capitalized(words: string): string {
  return words.charAt(0).toUpperCase() + words.slice(1);
}

// dayWords returns day, a YYYY-MM-DD date, as inbok writes a day, e.g.
// "14 Nov 2026".
export function dayWords(day: string | undefined): string | undefined {
  if (!day) return undefined;
  const [year, month, date] = day.split("-").map(Number);
  return dayDate.format(new Date(year, month - 1, date));
}
