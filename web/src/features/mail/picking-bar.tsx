import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
  type RefObject,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Toolbar } from "react-aria-components";
import { ArrowLeftIcon, EllipsisVerticalIcon } from "lucide-react";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import { accountName } from "@/components/account-mark";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DropdownMenu, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { mailData, type Conversation } from "./conversations";
import { offeredIn } from "./act";
import { requestRowFocus } from "./focus-request";
import { LabelMenuItems, LabelPicker, pickersFor, type Picker } from "./label-menu";
import { useLabels } from "./labels";
import { perform, type Performed } from "./perform";
import type { Picking } from "./picking";
import type { Place } from "./places";
import { actionsFor, changeable, changersOf, staysInList, targetsOf, type Action } from "./rules";
import { laidOut } from "./slots";
import {
  moreName,
  pickAllName,
  pickAllTooltip,
  pickedBarName,
  pickedNumber,
  pickedWord,
  unpickAll,
  stopPicking,
} from "./text";
import { actionWords } from "./words";

// PickingBar takes the list header's place while conversations are picked
// (DESIGN.md "Message list"), or a search's filter sentence's, and the top
// bar's on a phone (inTopBar),
// after an arrow that stops picking. It leads with a checkbox, mixed while
// some of the loaded rows are picked, that picks all loaded, or unpicks
// all once all are; then how many are picked, "All N" once the list has
// reached its end (complete); then the actions picked offers in the list
// of places, of account alone when given, as its reading pane offers them
// (actionsFor, offeredIn): each slot shows its usual action while any picked row
// needs it, and More holds Move to… and Labels… (LabelPicker), the pair's
// other action when the rows are mixed, and Report spam, shown while it
// holds any. An action goes to the picked rows it changes, of accounts
// that take actions (changersOf), the others keeping their pick;
// rows it takes out of the list stay picked until it is answered, so a
// refused one comes back picked, and focus goes to the list's next row
// (focusKey's) when none is left. heading keeps the place's name for
// screen readers, headingRef the page's focus target.
export function PickingBar({
  places,
  focusKey,
  account,
  name,
  headingRef,
  picked,
  loaded,
  complete,
  picking,
  inTopBar,
}: {
  places: readonly Place[];
  focusKey: string;
  account: number | undefined;
  name: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
  picked: readonly Conversation[];
  loaded: readonly Conversation[];
  complete: boolean;
  picking: Picking;
  inTopBar: boolean;
}): ReactElement {
  const client = useQueryClient();
  // The list read them already: the bar's arrival needs no read of its own.
  const accounts = useListConnectedAccounts({ query: { refetchOnMount: false } });
  const accountList = accounts.data?.data.accounts;
  const labels = useLabels();
  const names = useMemo(
    () => new Map(accountList?.map((a) => [a.id, accountName(a)])),
    [accountList],
  );

  const changers = useMemo(() => changersOf(accountList), [accountList]);
  // Each button keeps its slot as its key, so focus stays on it as its
  // action changes, e.g. Star to Remove star, as the toolbar's do.
  // The list whose actions picked rows offer: a place's own, or a search's.
  const place = offeredIn(places);
  const { buttons, more } = laidOut(actionsFor(picked, place, changers));
  // The picked rows whose accounts take actions, which Move to and Labels
  // change; the others keep their pick.
  const own = changeable(picked, changers);
  // No More while it would open empty: no picked row's account takes
  // actions.
  const offersMore = pickersFor(own, place).length > 0 || more.length > 0;
  // Whether focus was last on one of the actions, and the pick-all box,
  // which takes it when none is left, e.g. after Archive took the picked
  // rows of accounts that take actions and left only others: a focused
  // button that goes leaves focus on the page. The browser tells no one
  // as the button goes, so this is noted as focus moves.
  const onAction = useRef(false);
  const pickAllBox = useRef<HTMLSpanElement>(null);
  const offersNothing = buttons.length === 0 && !offersMore;
  useEffect(() => {
    if (!offersNothing || !onAction.current) return;
    onAction.current = false;
    if (document.activeElement !== document.body) return;
    pickAllBox.current?.querySelector("input")?.focus();
  }, [offersNothing]);

  const run = (action: Action) => act(action, targetsOf(picked, action, place, changers));
  // act runs performed on targets, the picked rows it changes.
  const act = (performed: Performed, targets: readonly Conversation[]) => {
    if (targets.length === 0) return;
    const ids = targets.map((c) => c.id);
    const changing = new Set(ids);
    const { conversations, placeCounts } = mailData(client);
    const emptied = picked.every(
      (row) => changing.has(row.id) && !staysInList(row, performed, places, account),
    );
    const release = picking.hold(ids);
    void perform({
      mail: { client, conversations, placeCounts },
      conversations: targets,
      act: performed,
      from: "picked",
      accountName: (id) => names.get(id) ?? "",
    }).then((outcomes) => {
      // A refused or failed row is back in the list, or about to be.
      release(ids.filter((id) => outcomes.get(id) === "refused" || outcomes.get(id) === "failed"));
    });
    // The bar goes with the last picked row, and focus with it: to the row
    // that took the first one's place.
    if (emptied) {
      const [first] = picked;
      requestRowFocus(focusKey, {
        id: first.id,
        dateMs: first.dateMs,
        stays: false,
        now: true,
      });
    }
  };

  const all = picked.length === loaded.length;

  // The label picker More opened, under More's button.
  const [picker, setPicker] = useState<Picker | null>(null);
  const moreButton = useRef<HTMLButtonElement>(null);
  const pickAll = () => picking.pick(all ? new Set() : new Set(loaded.map((row) => row.id)));
  return (
    <>
      {/* The place's heading stays for screen readers, which name the
          list's landmark by it, and for focus to return to. */}
      <h1 ref={headingRef} id="place-heading" tabIndex={-1} className="sr-only">
        {name}
      </h1>
      {/* Escape stops picking from the bar too, as it does from the list;
          a key from its More menu, drawn elsewhere, is the menu's. Drawn
          as its contents, so the bar lays out as before. */}
      {/* oxlint-disable-next-line jsx-a11y/no-static-element-interactions -- only hears Escape; the bar's controls are what act. */}
      <div
        className="contents"
        // In the bubble phase, after a showing tooltip, which takes the
        // first Escape on the document and stops it, at every width:
        // hiding a tooltip must not drop the picks (WCAG 1.4.13).
        onKeyDown={(event: KeyboardEvent<HTMLDivElement>) => {
          if (event.key !== "Escape" || !(event.target instanceof Node)) return;
          if (!event.currentTarget.contains(event.target)) return;
          event.preventDefault();
          picking.pick(new Set());
        }}
      >
        <Toolbar
          aria-label={pickedBarName}
          data-slot="picking-bar"
          // Wraps only when the actions do not fit beside the count, e.g.
          // a phone at large text, as the reading pane's toolbar does.
          className={cn(
            "flex flex-wrap items-center",
            inTopBar ? "min-w-0 flex-1 gap-2" : "shrink-0 gap-3 border-b py-2 pr-2 pl-4",
          )}
        >
          {inTopBar && (
            <TooltipTrigger>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={stopPicking}
                onPress={() => picking.pick(new Set())}
              >
                <ArrowLeftIcon aria-hidden="true" />
              </Button>
              <Tooltip>{stopPicking}</Tooltip>
            </TooltipTrigger>
          )}
          {/* Over the rows' avatars beside the list, so its box sits on
            their checkboxes' line. */}
          <span ref={pickAllBox} className={cn("flex shrink-0 justify-center", !inTopBar && "w-9")}>
            <TooltipTrigger>
              <Checkbox
                // As its tooltip says, so a voice command by what it shows
                // finds it (WCAG 2.5.3); its state says ticked or mixed.
                aria-label={all ? unpickAll : pickAllName(loaded.length)}
                // The bar's buttons' 32 px, 44 px to touch, so the bar is the
                // header's height.
                // As wide as it is tall, so its whole column presses it.
                className="*:min-h-8 *:min-w-8 *:justify-center pointer-coarse:*:min-h-11 pointer-coarse:*:min-w-11"
                isSelected={all}
                isIndeterminate={!all}
                onChange={pickAll}
              />
              <Tooltip>{all ? unpickAll : pickAllTooltip(loaded.length)}</Tooltip>
            </TooltipTrigger>
          </span>
          {/* The word wraps onto a second line, cut off, when the number
            and it don't fit, e.g. at a phone's touch-size buttons; screen
            readers still hear it. */}
          <span className="flex h-5 min-w-0 flex-1 flex-wrap overflow-hidden text-sm leading-5 font-semibold tabular-nums">
            <span className="min-w-0 truncate">{pickedNumber(picked.length, all && complete)}</span>
            {/* With touch-size buttons, the number alone, as phone mail
                apps show it: the word, which fits only now and then, would
                come and go. Screen readers hear it either way. */}
            <span className="whitespace-pre pointer-coarse:sr-only"> {pickedWord}</span>
          </span>
          {/* The list header's button size, as the bar takes its place.
            Never wider than the bar: where they do not fit beside the
            count, the actions take a row of their own and wrap in it, at
            its end, so none runs off the screen. */}
          <span
            className="ml-auto flex max-w-full shrink-0 flex-wrap items-center justify-end"
            onFocus={() => {
              onAction.current = true;
            }}
            onBlur={() => {
              onAction.current = false;
            }}
          >
            {buttons.map(({ slot, action }) => {
              const { label, icon: Icon } = actionWords[action];
              return (
                <TooltipTrigger key={slot}>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={label}
                    onPress={() => run(action)}
                  >
                    <Icon aria-hidden="true" />
                  </Button>
                  <Tooltip>{label}</Tooltip>
                </TooltipTrigger>
              );
            })}
            {offersMore && (
              <DropdownMenuTrigger>
                <TooltipTrigger>
                  <Button ref={moreButton} variant="ghost" size="icon-sm" aria-label={moreName}>
                    <EllipsisVerticalIcon aria-hidden="true" />
                  </Button>
                  <Tooltip>{moreName}</Tooltip>
                </TooltipTrigger>
                <DropdownMenu placement="bottom end" className="w-auto">
                  <LabelMenuItems conversations={own} list={place} onOpen={setPicker} />
                  {more.map((action) => {
                    const { label, icon: Icon } = actionWords[action];
                    return (
                      <DropdownMenuItem key={action} onAction={() => run(action)}>
                        <Icon aria-hidden="true" />
                        {label}
                      </DropdownMenuItem>
                    );
                  })}
                </DropdownMenu>
              </DropdownMenuTrigger>
            )}
            <LabelPicker
              picker={picker}
              triggerRef={moreButton}
              onClose={() => setPicker(null)}
              conversations={own}
              list={place}
              // Under their account's name where the list shows several
              // accounts' mail, even once one account's picks are left.
              headings={account === undefined && (accountList?.length ?? 0) > 1}
              labels={labels}
              accountName={(id) => names.get(id) ?? ""}
              canChange={(id) => changers.has(id)}
              onAct={act}
            />
          </span>
        </Toolbar>
      </div>
    </>
  );
}
