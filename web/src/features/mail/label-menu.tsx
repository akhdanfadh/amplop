import { useEffect, useRef, useState, type ReactElement, type RefObject } from "react";
import {
  Autocomplete,
  Dialog,
  Heading,
  Menu,
  Popover,
  SearchField,
  useFilter,
  type Selection,
} from "react-aria-components";
import { CheckIcon, MinusIcon, PlusIcon, SearchIcon, XIcon } from "lucide-react";
import {
  conversationChangeAddMax,
  conversationChangeRemoveMax,
} from "@/api/generated/model/conversationChange.zod";
import { Button } from "@/components/ui/button";
import {
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
} from "@/components/ui/dropdown-menu";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import { cn } from "@/lib/cn";
import type { Conversation } from "./conversations";
import { canMoveFrom, type Label } from "./labels";
import { LabelDialog, sameText, splitTyped, type LabelStart } from "./label-dialog";
import { isViewPlace, type Place } from "./places";
import type { ConversationChange } from "./rules";
import type { Performed } from "./perform";
import {
  applyLabels,
  clearFilter,
  filterLabels,
  labelsLimit,
  labelsMenuName,
  moveMenuName,
  newLabelName,
  newLabelNamed,
  noLabelMatches,
  noLabelsIn,
  noLabelsInThese,
  noLabelsInTheseToMake,
  noLabelsToMake,
  noLabelsYetIn,
  noLabelsYetToMake,
  onSome,
  opensPicker,
} from "./text";
import { labelsName, labelWords, moveTo, moveWords } from "./words";

// Section is one account's labels in a menu, under its name when the
// menu shows several accounts' or the list spans accounts.
type Section = { accountId: number; name: string; labels: Label[] };

// Picker is which label picker More opens: Move to's or Labels'.
export type Picker = "move" | "labels";

// labelable returns the conversations, of conversations, whose labels
// their rows show: those with a message outside Spam and Trash.
//
// DESIGN: a workaround, not a rule. Gmail keeps a trashed message's
// labels, and the API changes them, but a row wholly in Spam or Trash
// lists no labels, as the server keeps one list of places for both which
// lists hold a row and which labels it has (ADR031, "Labels as built";
// DEBT.md, "Keep a conversation's labels apart from the lists that hold
// it"). Labels there would change what no screen shows.
export function labelable(conversations: readonly Conversation[]): Conversation[] {
  return conversations.filter((c) => c.allMail);
}

// pickersFor returns the pickers a More menu offers for conversations in
// list: Move to while there are any and they can leave list, the place of
// the list they are in (canMoveFrom); Labels while any of them is
// labelable.
export function pickersFor(conversations: readonly Conversation[], list: Place): Picker[] {
  return [
    ...(conversations.length > 0 && canMoveFrom(list) ? (["move"] as const) : []),
    ...(labelable(conversations).length > 0 ? (["labels"] as const) : []),
  ];
}

// LabelMenuItems are a More menu's Move to… and Labels… (DESIGN.md
// "Reading pane") for conversations, as pickersFor offers them, each
// opening its picker (onOpen).
export function LabelMenuItems({
  conversations,
  list,
  onOpen,
}: {
  conversations: readonly Conversation[];
  list: Place;
  onOpen: (picker: Picker) => void;
}): ReactElement {
  const pickers = pickersFor(conversations, list);
  return (
    <>
      {pickers.includes("move") && (
        <DropdownMenuItem textValue={moveTo.label} onAction={() => onOpen("move")}>
          <moveTo.icon aria-hidden="true" />
          {opensPicker(moveTo.label)}
        </DropdownMenuItem>
      )}
      {pickers.includes("labels") && (
        <DropdownMenuItem textValue={labelsName.label} onAction={() => onOpen("labels")}>
          <labelsName.icon aria-hidden="true" />
          {opensPicker(labelsName.label)}
        </DropdownMenuItem>
      )}
    </>
  );
}

// LabelPicker is the open picker, of picker, under More's button
// (triggerRef), in More's place, so it fits any width (ADR031, "Labels as
// built"): conversations' accounts' labels, one section per account,
// headed by its name when headings is set, e.g. a list of every
// account's. labels are the member's, as the screen reads them; onAct
// runs what the member chose on the conversations it changes; onClose
// closes it, which Escape and a press outside also do, sending nothing.
// accountName names an account; canChange reports whether an account's
// labels can change, as its allowedActions say, where typing a name no
// label has offers New label, which opens its dialog over the picker
// (LabelDialog): from Labels the label made comes back ticked, waiting for
// Apply; from Move to, the dialog's button moves there once it is made.
export function LabelPicker({
  picker,
  triggerRef,
  onClose,
  ...body
}: {
  picker: Picker | null;
  triggerRef: RefObject<HTMLElement | null>;
  onClose: () => void;
  conversations: readonly Conversation[];
  list: Place;
  headings: boolean;
  labels: readonly Label[];
  accountName: (accountId: number) => string;
  canChange: (accountId: number) => boolean;
  onAct: (act: Performed, targets: Conversation[]) => void;
}): ReactElement {
  // The New label the picker opened last: what it starts with, in which
  // accounts, and what the label made goes on to; kept while it closes, so
  // its form keeps its values as it fades.
  const [making, setMaking] = useState<Making | null>(null);
  const [makingOpen, setMakingOpen] = useState(false);
  // 28rem, in pixels as React Aria takes it, read as it opens, so the
  // cap grows with the text size (WCAG 1.4.4) as the list's rows do.
  const maxHeight =
    picker === null
      ? undefined
      : 28 * Number.parseFloat(getComputedStyle(document.documentElement).fontSize);
  // close closes the picker, focus back on More's button or left where
  // what the member chose puts it, e.g. a moved row's list.
  const close = (focus: FocusAfter) => {
    onClose();
    if (focus === "left") return;
    // NOTE: React Aria hands focus back to what had it as the picker
    // opened, More's item, gone with its menu, so focus would fall to the
    // page: it goes to More's button instead, once the picker is gone,
    // unless something, e.g. a press outside, put it somewhere.
    requestAnimationFrame(() => {
      const trigger = triggerRef.current;
      const lost = document.activeElement === null || document.activeElement === document.body;
      if (lost && trigger?.isConnected) trigger.focus();
    });
  };
  return (
    <>
      <Popover
        triggerRef={triggerRef}
        isOpen={picker !== null}
        onOpenChange={(open) => {
          if (!open) close("back");
        }}
        placement="bottom end"
        offset={4}
        maxHeight={maxHeight}
        // No entrance: it opens in More's place as More closes, and two
        // surfaces fading at one spot read as one muddy one.
        className="z-50 flex w-60 max-w-[calc(100vw-2rem)] origin-(--trigger-anchor-point) flex-col overflow-hidden rounded-xl bg-popover text-popover-foreground shadow-popover ring-1 ring-border duration-200 ease-standard outline-none data-exiting:animate-out data-exiting:fade-out-0 data-exiting:zoom-out-95 **:data-[slot$=-item]:data-focused:bg-foreground/10 dark:ring-foreground/20 forced-colors:outline-1 forced-colors:outline-solid"
      >
        <Dialog className="flex min-h-0 flex-col outline-hidden">
          {/* Its title names it, so Move to, which acts on a press, and
            Labels, which waits for Apply, read apart once More closes. */}
          {picker && (
            <>
              <Heading slot="title" className="px-2.5 pt-2 pb-1 text-sm font-medium">
                {picker === "move" ? moveMenuName : labelsMenuName}
              </Heading>
              <PickerBody
                picker={picker}
                onClose={close}
                onMake={(next) => {
                  setMaking(next);
                  setMakingOpen(true);
                }}
                {...body}
              />
            </>
          )}
        </Dialog>
      </Popover>
      {/* Beside the picker, not in it, so Escape in the dialog closes only
        the dialog: React's events bubble through the tree a portal sits
        in. */}
      <LabelDialog
        isOpen={makingOpen}
        onOpenChange={setMakingOpen}
        labels={body.labels}
        accountIds={making?.accountIds ?? []}
        start={making?.start}
        action={picker === "move" ? "create-and-move" : "create"}
        onDone={(label) => making?.done(label)}
      />
    </>
  );
}

// Making is a New label a picker opened: what its form starts with, the
// accounts it can be made in, and done, which takes the label made.
type Making = {
  start: LabelStart;
  accountIds: readonly number[];
  done: (label: Label) => void;
};

// FocusAfter is where focus goes as a picker closes: back to More's
// button, or left where what the member chose puts it.
type FocusAfter = "back" | "left";

// PickerBody is an open picker's list, of LabelPicker's props, closing it
// with onClose, which says where focus goes. Mounted while it is open, it
// keeps the labels and Labels' ticks as they were as it opened, so a
// label read meanwhile changes nothing the member ticked.
function PickerBody({
  picker,
  onClose,
  onMake,
  conversations: given,
  list,
  headings,
  labels: givenLabels,
  accountName,
  canChange,
  onAct,
}: {
  picker: Picker;
  onClose: (focus: FocusAfter) => void;
  onMake: (making: Making) => void;
  conversations: readonly Conversation[];
  list: Place;
  headings: boolean;
  labels: readonly Label[];
  accountName: (accountId: number) => string;
  canChange: (accountId: number) => boolean;
  onAct: (act: Performed, targets: Conversation[]) => void;
}): ReactElement {
  const [labels, setLabels] = useState(givenLabels);
  // The member's labels as last read, which place a label made here in the
  // tree: New label reads them again before it hands the label back.
  const latest = useRef(givenLabels);
  useEffect(() => {
    latest.current = givenLabels;
  });
  // Labels changes only the labelable ones (labelable).
  const [conversations] = useState(() => (picker === "labels" ? labelable(given) : [...given]));
  const [initial, setInitial] = useState(() => ticksOf(labels, conversations));
  const [ticks, setTicks] = useState(initial);
  // React Aria hands the whole selection after a press; the label pressed
  // is the one whose ticked state it flipped.
  const press = (keys: Selection) =>
    setTicks((now) => {
      const selected = keys === "all" ? new Set(now.keys()) : keys;
      const flipped = [...now].filter(([place, tick]) => selected.has(place) !== (tick === "all"));
      // A press flips one label; select all, or anything else that flips
      // several, is not a member's tick here.
      if (flipped.length !== 1) return now;
      const [[place, tick]] = flipped;
      return new Map(now).set(place, nextTick(tick, initial.get(place) ?? "none"));
    });
  // In the sidebar's order: List Connected Accounts and List Places give
  // accounts oldest first, and IDs grow with age.
  // oxlint-disable-next-line unicorn/no-array-sort -- a new array; tsconfig's lib has no toSorted.
  const accounts = [...new Set(conversations.map((c) => c.accountId))].sort((a, b) => a - b);
  const sectionsOf = (leaving?: string): Section[] =>
    accounts.map((accountId) => ({
      accountId,
      name: accountName(accountId),
      labels: labels.filter((l) => l.accountId === accountId && l.place !== leaving),
    }));
  const of = (accountId: number) => conversations.filter((c) => c.accountId === accountId);
  // The accounts a label can be made in here.
  const makeIn = accounts.filter(canChange);

  const move = (label: Label) => {
    const targets = of(label.accountId);
    const change: ConversationChange = {
      ids: targets.map((c) => c.id),
      add: [label.place],
      remove: [list],
    };
    // The row leaves the list, whose own focus moves on, as Archive's.
    onClose("left");
    onAct({ changes: [change], words: moveWords(label.path) }, targets);
  };

  // What an account's ticks add and remove, against how they started.
  const changeOf = (accountId: number) => {
    const own = labels.filter((l) => l.accountId === accountId);
    return {
      add: own.filter((l) => ticks.get(l.place) === "all" && initial.get(l.place) !== "all"),
      remove: own.filter((l) => ticks.get(l.place) === "none" && initial.get(l.place) !== "none"),
    };
  };
  // An account's change adds and removes at most the contract's number of
  // places each, so Apply waits until the ticks fit.
  const overLimit = accounts.some((accountId) => {
    const { add, remove } = changeOf(accountId);
    return add.length > conversationChangeAddMax || remove.length > conversationChangeRemoveMax;
  });

  const apply = () => {
    const changes: ConversationChange[] = [];
    const added: string[] = [];
    const removed: string[] = [];
    for (const accountId of accounts) {
      const { add, remove } = changeOf(accountId);
      if (add.length === 0 && remove.length === 0) continue;
      changes.push({
        ids: of(accountId).map((c) => c.id),
        add: add.map((l) => l.place),
        remove: remove.map((l) => l.place),
      });
      added.push(...add.map((l) => l.path));
      removed.push(...remove.map((l) => l.path));
    }
    // The conversation stays: focus goes back where it came from.
    onClose("back");
    if (changes.length === 0) return;
    const ids = new Set(changes.flatMap((c) => c.ids));
    onAct(
      { changes, words: labelWords([...new Set(added)], [...new Set(removed)]) },
      conversations.filter((c) => ids.has(c.id)),
    );
  };

  // made takes a label New label made: Move to moves there, as choosing
  // it would; Labels lists it, ticked, among the labels it opened with.
  const made = (label: Label) => {
    if (picker === "move") {
      move(label);
      return;
    }
    setLabels((now) => withLabel(now, label, latest.current));
    setInitial((now) => new Map(now).set(label.place, "none"));
    setTicks((now) => new Map(now).set(label.place, "all"));
  };
  // make opens New label for typed, in the first account it can be made
  // in, typed split at a label it names.
  const make =
    makeIn.length === 0
      ? undefined
      : (typed: string) => {
          const [accountId] = makeIn;
          onMake({
            start: { accountId, ...splitTyped(typed, labels, accountId) },
            accountIds: makeIn,
            done: made,
          });
        };

  return picker === "move" ? (
    <MoveMenu
      sections={sectionsOf(isViewPlace(list) ? undefined : list)}
      headings={headings}
      onMove={move}
      onMake={make}
    />
  ) : (
    <LabelsMenu
      sections={sectionsOf()}
      headings={headings}
      ticks={ticks}
      changed={[...ticks].some(([place, tick]) => tick !== initial.get(place))}
      overLimit={overLimit}
      onPress={press}
      onApply={apply}
      onMake={make}
    />
  );
}

// withLabel returns labels, in the sidebar's order, with label added where
// read, the member's labels as last read, place it: after the last of
// labels that comes before it there, so under its parent in name order.
// Without it in read, it goes last.
function withLabel(labels: readonly Label[], label: Label, read: readonly Label[]): Label[] {
  const order = new Map(read.map((l, i) => [l.place, i]));
  const at = order.get(label.place);
  if (at === undefined) return [...labels, label];
  let after = -1;
  labels.forEach((l, i) => {
    if ((order.get(l.place) ?? Infinity) < at) after = i;
  });
  return [...labels.slice(0, after + 1), label, ...labels.slice(after + 1)];
}

// FilterField narrows a label menu as the member types; arrow keys still
// move through the menu. It takes focus as the menu opens, but not from a
// touch, where the keyboard would cover the labels.
function FilterField(): ReactElement {
  const [touch] = useState(() => window.matchMedia("(pointer: coarse)").matches);
  return (
    <SearchField
      aria-label={filterLabels}
      // A searchable menu opens to its field, as a searchable select does
      // (select.tsx), so typing filters at once.
      // oxlint-disable-next-line jsx-a11y/no-autofocus
      autoFocus={!touch}
      className="group/filter p-1"
    >
      <InputGroup>
        <InputGroupAddon>
          <SearchIcon aria-hidden="true" />
        </InputGroupAddon>
        <InputGroupInput
          placeholder={filterLabels}
          // Not the browser's own clear button: Escape clears, as React
          // Aria's field does, and ours shows on a phone, which has none.
          className="[&::-webkit-search-cancel-button]:appearance-none"
        />
        {/* React Aria's field clears itself from a button in it, as the
            search box's Clear search does. */}
        <InputGroupAddon align="inline-end" className="group-data-empty/filter:hidden">
          <InputGroupButton size="icon-sm" aria-label={clearFilter}>
            <XIcon aria-hidden="true" />
          </InputGroupButton>
        </InputGroupAddon>
      </InputGroup>
    </SearchField>
  );
}

// LabelMenuBody is a label menu's filter and list: sections' labels, each
// a LabelItem, ticked from ticks in Labels or running onMove in Move to,
// with the empty state's words when no label shows: none match the
// filter, or the accounts have none yet. With onMake, typing a name no
// label shown has offers New label for it last, which runs onMake.
function LabelMenuBody({
  name,
  sections,
  headings,
  menu,
  ticks,
  onMove,
  onMake,
}: {
  name: string;
  sections: readonly Section[];
  headings: boolean;
  menu?: {
    selectionMode: "multiple";
    selectedKeys: Selection;
    onSelectionChange: (keys: Selection) => void;
    escapeKeyBehavior: "none";
    shouldCloseOnSelect: false;
  };
  ticks?: ReadonlyMap<string, Tick>;
  onMove?: (label: Label) => void;
  onMake?: (typed: string) => void;
}): ReactElement {
  const [query, setQuery] = useState("");
  const { contains } = useFilter({ sensitivity: "base" });
  const typed = query.trim();
  const filtering = typed !== "";
  const shown = sections.filter((s) => s.labels.length > 0);
  const bare = sections.filter((s) => s.labels.length === 0).map((s) => s.name);
  const none =
    sections.length === 1
      ? (onMake ? noLabelsToMake : noLabelsIn)(sections[0].name)
      : onMake
        ? noLabelsInTheseToMake
        : noLabelsInThese;
  // Hidden while a label shown has the very name typed, which the server
  // would refuse as taken; whether a name is taken is the server's rule.
  const offerMake =
    onMake !== undefined &&
    filtering &&
    !shown.some((s) => s.labels.some((l) => sameText(l.path, typed)));
  // Nothing to filter or make: the words alone, in a menu so the submenu
  // reads as one.
  if (shown.length === 0 && !onMake) {
    return (
      <Menu
        aria-label={name}
        className="p-1 outline-hidden"
        renderEmptyState={() => <p className="px-1.5 py-2 text-sm text-muted-foreground">{none}</p>}
      >
        {[]}
      </Menu>
    );
  }
  return (
    <Autocomplete filter={contains} inputValue={query} onInputChange={setQuery}>
      <FilterField />
      <Menu
        aria-label={name}
        {...menu}
        // As a list's menus: a long one's end leaves the page still
        // (dropdown-menu.tsx).
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain border-t p-1 outline-hidden"
        renderEmptyState={() => (
          <p className="px-1.5 py-2 text-sm text-muted-foreground">
            {filtering ? noLabelMatches : none}
          </p>
        )}
      >
        {shown.map((section) => (
          <DropdownMenuGroup key={section.accountId} aria-label={section.name}>
            {headings && <DropdownMenuLabel>{section.name}</DropdownMenuLabel>}
            {section.labels.map((label) => (
              <LabelItem
                key={label.place}
                label={label}
                filtering={filtering}
                tick={ticks?.get(label.place)}
                onMove={onMove}
              />
            ))}
          </DropdownMenuGroup>
        ))}
        {offerMake && (
          // Its own section, which no selection reaches: in Labels a press
          // opens New label, never ticks.
          <DropdownMenuGroup aria-label={newLabelName} selectionMode="none">
            <DropdownMenuItem
              id={makeKey}
              // The name typed, so the filter always keeps it.
              textValue={typed}
              aria-label={newLabelNamed(typed)}
              className="items-start"
              onAction={() => onMake?.(typed)}
            >
              <PlusIcon aria-hidden="true" className="mt-0.5" />
              <span dir="auto" className="min-w-0 [overflow-wrap:anywhere]">
                {newLabelName} <span className="font-medium">&quot;{typed}&quot;</span>
                {opensPicker("")}
              </span>
            </DropdownMenuItem>
          </DropdownMenuGroup>
        )}
      </Menu>
      {/* Accounts of the conversations with no labels say why they offer
          none, rather than leave their rows unexplained. Under the list:
          React Aria drops a section with no items. */}
      {!filtering && shown.length > 0 && bare.length > 0 && (
        <p className="border-t px-2.5 py-2 text-sm text-muted-foreground">
          {(onMake ? noLabelsYetToMake : noLabelsYetIn)(bare)}
        </p>
      )}
    </Autocomplete>
  );
}

// The key of a picker's New label item, which no label's id can be.
const makeKey = "new-label";

// treeStep is how far a label sits in from its parent, in rem, so it
// grows with the text.
const treeStep = 0.875;
// treeStart is the menu item's own inline padding, in rem. While the
// whole tree shows, a label sits in by its depth; while the member
// filters, not at all, as matches then show their paths.
const treeStart = 0.375;

// TreeName is a label's name in a menu: its own, after a line for each
// parent, as the sidebar's labels show it; while the member filters, its
// path, the parents' names muted.
function TreeName({ label, filtering }: { label: Label; filtering: boolean }): ReactElement {
  const parents = label.path.slice(0, label.path.length - label.name.length);
  return (
    <>
      {!filtering &&
        Array.from({ length: label.depth }, (_, level) => (
          <span
            key={level}
            aria-hidden="true"
            className="pointer-events-none absolute inset-y-0 border-l border-border"
            style={{ left: `${treeStart + level * treeStep + 0.25}rem` }}
          />
        ))}
      <PathName parents={filtering ? parents : ""} name={label.name} />
    </>
  );
}

// PathName is a label's name, after its parents' names, muted, when
// parents is set. A long one wraps rather than being cut, so every label
// can be told apart on every screen (DESIGN.md "Reading pane"), and a
// name without spaces, e.g. an address, breaks at the edge. A member's
// own words, which a browser's translation leaves alone.
export function PathName({ parents, name }: { parents: string; name: string }): ReactElement {
  return (
    <span dir="auto" translate="no" className="min-w-0 [overflow-wrap:anywhere]">
      {parents && (
        // A hovered chip's and a focused item's fills take muted text
        // under 4.5:1, so there its parents take the name's colour.
        <span className="text-muted-foreground group-data-focused/label:text-foreground group-data-hovered/badge:text-foreground">
          {parents}
        </span>
      )}
      {name}
    </span>
  );
}

// MoveMenu is Move to's list: choosing a label runs onMove at once; onMake
// opens New label (LabelMenuBody).
function MoveMenu({
  sections,
  headings,
  onMove,
  onMake,
}: {
  sections: readonly Section[];
  headings: boolean;
  onMove: (label: Label) => void;
  onMake: ((typed: string) => void) | undefined;
}): ReactElement {
  return (
    <LabelMenuBody
      name={moveMenuName}
      sections={sections}
      headings={headings}
      onMove={onMove}
      onMake={onMake}
    />
  );
}

// LabelItem is one label in a label menu: with its tick, in Labels, or
// moving to it on onMove, in Move to.
function LabelItem({
  label,
  filtering,
  tick,
  onMove,
}: {
  label: Label;
  filtering: boolean;
  tick: Tick | undefined;
  onMove: ((label: Label) => void) | undefined;
}): ReactElement {
  return (
    <DropdownMenuItem
      id={label.place}
      // The path, for typing to find and for screen readers: two labels
      // can share a name under different parents. A label on some says
      // so; checked or not, React Aria says.
      textValue={label.path}
      aria-label={tick === "some" ? `${label.path}, ${onSome}` : label.path}
      style={filtering ? undefined : { paddingLeft: `${treeStart + label.depth * treeStep}rem` }}
      // In Labels, its own box at the start, as a checkbox list's: not
      // the menu's tick at the end, which has no "some".
      // A name on several lines starts beside its box, as a checkbox's
      // label does.
      className={cn(
        "group/label items-start",
        tick && "gap-2 pr-1.5 *:data-[slot=dropdown-menu-checkbox-item-indicator]:hidden",
      )}
      onAction={onMove && (() => onMove(label))}
    >
      {tick && <TickBox tick={tick} />}
      <TreeName label={label} filtering={filtering} />
    </DropdownMenuItem>
  );
}

// Tick is how many of the conversations a label is on, as Labels shows
// it: all, some, or none.
type Tick = "all" | "some" | "none";

// nextTick returns a label's tick after a press: a label on some goes on
// all, then none, then back to some, as it was; another flips.
function nextTick(now: Tick, was: Tick): Tick {
  if (was !== "some") return now === "all" ? "none" : "all";
  return now === "some" ? "all" : now === "all" ? "none" : "some";
}

// ticksOf returns how many of conversations each of labels is on, among
// those of its account: all, some, or none.
function ticksOf(
  labels: readonly Label[],
  conversations: readonly Conversation[],
): Map<string, Tick> {
  const ticks = new Map<string, Tick>();
  for (const label of labels) {
    const own = conversations.filter((c) => c.accountId === label.accountId);
    if (own.length === 0) continue;
    const on = own.filter((c) => c.labelIds.includes(label.place)).length;
    ticks.set(label.place, on === 0 ? "none" : on === own.length ? "all" : "some");
  }
  return ticks;
}

// LabelsMenu is Labels' list: sections' labels with ticks, which onPress
// changes, and Apply, enabled once something changed, which runs onApply
// to send them together, unless overLimit, which it says. Closing it any
// other way sends nothing. onMake opens New label (LabelMenuBody).
function LabelsMenu({
  sections,
  headings,
  ticks,
  changed,
  overLimit,
  onPress,
  onApply,
  onMake,
}: {
  sections: readonly Section[];
  headings: boolean;
  ticks: ReadonlyMap<string, Tick>;
  changed: boolean;
  overLimit: boolean;
  onPress: (keys: Selection) => void;
  onApply: () => void;
  onMake: ((typed: string) => void) | undefined;
}): ReactElement {
  const selected = new Set([...ticks].filter(([, t]) => t === "all").map(([place]) => place));
  return (
    <>
      <LabelMenuBody
        name={labelsMenuName}
        sections={sections}
        headings={headings}
        menu={{
          selectionMode: "multiple",
          selectedKeys: selected,
          onSelectionChange: onPress,
          // React Aria's menus clear a multiple selection on Escape, which
          // here would untick every label; Escape only closes.
          escapeKeyBehavior: "none",
          // Enter ticks, as a press does: React Aria's menus close on
          // Enter, which would drop the ticks before Apply.
          shouldCloseOnSelect: false,
        }}
        ticks={ticks}
        onMake={onMake}
      />
      {(ticks.size > 0 || onMake) && (
        <div className="flex items-center justify-end gap-2 border-t p-1">
          {overLimit && (
            <p className="mr-auto px-1.5 text-sm text-muted-foreground">
              {labelsLimit(conversationChangeAddMax)}
            </p>
          )}
          <Button size="sm" isDisabled={!changed || overLimit} onPress={onApply}>
            {applyLabels}
          </Button>
        </div>
      )}
    </>
  );
}

// TickBox draws a label's tick as checkbox.tsx draws a box: ticked, a
// dash for some, or empty.
function TickBox({ tick }: { tick: Tick }): ReactElement {
  return (
    <span
      aria-hidden="true"
      className={cn(
        // On the name's first line: 2 px down a 20 px line.
        "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-sm border border-input bg-card transition-colors [&>svg]:size-3.5",
        // The menu item's focus colours its icons; the box keeps its own.
        tick !== "none" &&
          "border-primary bg-primary [&>svg]:text-primary-foreground! forced-colors:[&>svg]:text-[CanvasText]!",
      )}
    >
      {tick === "all" ? <CheckIcon /> : tick === "some" ? <MinusIcon /> : null}
    </span>
  );
}
