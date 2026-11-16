import {
  Fragment,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type RefObject,
} from "react";
import { linkOptions, useLocation, useMatch } from "@tanstack/react-router";
import { cn } from "@/lib/cn";
import { formatCount } from "@/lib/count";
import { Disclosure, DisclosurePanel } from "react-aria-components";
import {
  ArchiveIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  InboxIcon,
  LayersIcon,
  PencilIcon,
  PlusIcon,
  SendIcon,
  ShieldAlertIcon,
  StarIcon,
  Trash2Icon,
  UploadIcon,
  type LucideIcon,
} from "lucide-react";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccount } from "@/api/generated/model";
import { accountDot, accountName } from "@/components/account-mark";
import { NavItem, within } from "@/components/nav-item";
import type { Tone } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { refreshInterval } from "@/features/connected-accounts/sync";
import { tryAgainText } from "@/features/connected-accounts/text";
import { isViewPlace, viewPlaces, type AppHref, type ViewPlace } from "./places";
import { accountDetail, accountStatus } from "./account-status";
import { accountFold, labelFold, setUnfolded, unfold, useUnfolded, type FoldKey } from "./folds";
import { useOverflow } from "@/hooks/use-overflow";
import { labelTree, unreadIn, usePlaceCounts, type LabelNode } from "./place-counts";
import { labelsOf, type Label } from "./labels";
import { LabelDialog } from "./label-dialog";
import { DeleteLabelDialog, LabelActionsMenu, type LabelAsk } from "./label-actions";
import type { PlaceCount } from "./conversations";
import { useOutbox } from "@/features/outgoing-messages/outbox";
import { useCompose } from "@/features/outgoing-messages/compose-store";
import {
  accountFoldName,
  accountsName,
  accountsUnreadable,
  allAccountsName,
  composeName,
  labelFoldName,
  newLabelName,
  notSentLabel,
  outboxName,
  placeName,
  unreadLabel,
} from "./text";

const placeIcon: Record<ViewPlace, LucideIcon> = {
  inbox: InboxIcon,
  starred: StarIcon,
  sent: SendIcon,
  all: ArchiveIcon,
  spam: ShieldAlertIcon,
  trash: Trash2Icon,
};

// MailNav is the mail sidebar's content (DESIGN.md "Sidebar"): Compose;
// the views, the Inbox with its unread count, and after Sent the Outbox
// with how many of its messages are not sent yet; then the accounts, each
// filtering every view, with its inbox's unread count, its status, and its
// labels, and New label. The items of the open place and account filter
// are marked. New label, and a label's Edit and Delete, open their dialog
// over the page.
export function MailNav(): ReactElement {
  const pathname = useLocation({ select: (location) => location.pathname });
  const { dismiss: closeDrawer } = useSidebar();
  const outbox = useOutbox();
  // What still asks for the member, so a message sent again stops counting.
  const notSent = outbox.data?.data.messages.filter((message) => message.outstanding).length ?? 0;
  const open = useMatch({ from: "/_app/_mail/mail/$place", shouldThrow: false });
  const filter = open?.search.account;
  const counts = usePlaceCounts();
  const unfolded = useUnfolded();
  const box = useRef<HTMLDivElement>(null);
  useUnfoldOpenPlace(open?.params.place, filter, counts);
  // Once per place, so opening a conversation leaves the sidebar where
  // the member put it; a page with no place, e.g. the Outbox, by address.
  useScrollToOpenPlace(box, `${open?.params.place ?? pathname} ${filter}`, counts, unfolded);
  const accounts = useListConnectedAccounts({
    query: {
      // Often while a sync moves, so its status shows how it goes.
      refetchInterval: (query) => refreshInterval(query.state.data?.data, Date.now(), 0),
    },
  });
  const listed = accounts.data?.data.accounts ?? [];
  const labels = useMemo(() => labelsOf(counts), [counts]);
  // What the member asked to do to their labels, whose dialog shows while
  // tasking; kept while it closes, so it keeps its words as it fades.
  const [task, setTask] = useState<LabelTask | null>(null);
  const [tasking, setTasking] = useState(false);
  // accountRow returns accountId's row, where focus goes once the row that
  // opened a label's dialog went, e.g. with the label it deleted or moved.
  const accountRow = (accountId: number | undefined) =>
    accountId === undefined
      ? null
      : (box.current?.querySelector<HTMLElement>(`[data-account-row="${accountId}"]`) ?? null);
  const startTask = (ask: LabelAsk | "new") => {
    const label = ask === "new" ? undefined : labels.find((l) => l.place === ask.place);
    if (ask === "new") setTask({ kind: "new" });
    else if (label) setTask({ kind: ask.kind, label });
    else return;
    setTasking(true);
  };
  // The accounts whose labels the member can change, as the server says.
  const changeable = listed.filter((a) => a.allowedActions.includes("change-mail"));

  return (
    // Its groups are the sidebar content's own, as if this box were not
    // there; it is only what useScrollToOpenPlace searches.
    <div ref={box} className="contents">
      <SidebarGroup>
        <ComposeButton onPress={closeDrawer} />
      </SidebarGroup>
      <SidebarGroup>
        <SidebarMenu>
          {viewPlaces.map((place) => (
            <Fragment key={place}>
              <NavItem
                // The account filter stays as the member moves between views.
                href={linkOptions({
                  to: "/mail/$place",
                  params: { place },
                  search: { account: filter },
                })}
                name={placeName[place]}
                icon={placeIcon[place]}
                isActive={within(pathname, `/mail/${place}`)}
                onPress={closeDrawer}
                count={
                  place === "inbox"
                    ? { value: unreadIn(counts, "inbox", filter), label: unreadLabel, unread: true }
                    : undefined
                }
              />
              {/* What is on its way to Sent, beside it and above the
                  accounts' labels, so its count stays in sight. It is
                  inbok's record, not Gmail's mail: every account's, so
                  the account filter doesn't reach it. */}
              {place === "sent" && (
                <NavItem
                  href={linkOptions({ to: "/mail/outbox" })}
                  name={outboxName}
                  icon={UploadIcon}
                  count={{ value: notSent, label: notSentLabel }}
                  isActive={within(pathname, "/mail/outbox")}
                  onPress={closeDrawer}
                />
              )}
            </Fragment>
          ))}
        </SidebarMenu>
      </SidebarGroup>
      <AccountsGroup
        accounts={listed}
        // A failed first read says so; a failed later one keeps the list.
        failed={!accounts.data && accounts.isError}
        retrying={accounts.isFetching}
        onRetry={() => void accounts.refetch()}
        counts={counts}
        place={open?.params.place}
        filter={filter}
        pathname={pathname}
        onPress={closeDrawer}
        onTask={startTask}
      />
      <LabelDialog
        isOpen={tasking && task?.kind !== "delete"}
        onOpenChange={setTasking}
        labels={labels}
        accountIds={changeable.map((a) => a.id)}
        start={
          task?.kind === "new"
            ? {
                // The account the sidebar shows, when it can take one.
                accountId: changeable.some((a) => a.id === filter)
                  ? (filter ?? 0)
                  : (changeable[0]?.id ?? 0),
                name: "",
                parent: null,
              }
            : undefined
        }
        editing={task?.kind === "edit" ? task.label : undefined}
        fallbackFocus={() => accountRow(task?.kind === "new" ? undefined : task?.label.accountId)}
        onDone={(label) => unfold(foldsTo(label, labels))}
      />
      <DeleteLabelDialog
        isOpen={tasking && task?.kind === "delete"}
        label={task?.kind === "delete" ? task.label : undefined}
        labels={labels}
        onClose={() => setTasking(false)}
        fallbackFocus={() => accountRow(task?.kind === "delete" ? task.label.accountId : undefined)}
      />
    </div>
  );
}

// LabelTask is what the member asked to do to their labels from the
// sidebar: make one, or edit or delete one, as it was when asked.
type LabelTask = { kind: "new" } | { kind: "edit" | "delete"; label: Label };

// foldsTo returns the folds to open so label shows in the sidebar: its
// account's, and each of its parents', found among labels by path.
function foldsTo(label: Label, labels: readonly Label[]): FoldKey[] {
  const keys: FoldKey[] = [accountFold(label.accountId)];
  const names = label.path.split("/");
  for (let end = 1; end < names.length; end++) {
    const path = names.slice(0, end).join("/");
    const parent = labels.find((l) => l.accountId === label.accountId && l.path === path);
    if (parent) keys.push(labelFold(parent.place));
  }
  return keys;
}

// ComposeButton is the one blue button: it opens the compose window on a
// new message, or brings back the one being written. onPress runs too,
// e.g. to close the drawer.
function ComposeButton({ onPress }: { onPress: () => void }): ReactElement {
  const { rail } = useSidebar();
  const compose = useCompose().store;

  return (
    <TooltipTrigger isDisabled={!rail}>
      <Button
        onPress={() => {
          onPress();
          compose.open();
        }}
        aria-label={rail ? composeName : undefined}
        // The pencil keeps its place open or as a rail, in line with the
        // icons below; the button keeps its height, so they don't move.
        className={cn(
          "w-full justify-start border-0 pl-2 pointer-coarse:pl-3.5",
          rail && "h-9 w-8 justify-center p-0 pointer-coarse:size-11 pointer-coarse:p-0",
        )}
      >
        <PencilIcon aria-hidden="true" />
        {!rail && composeName}
      </Button>
      <Tooltip placement="right">{composeName}</Tooltip>
    </TooltipTrigger>
  );
}

// The text colour of each status tone, as its badge's (badge.tsx).
const toneText: Record<Tone, string> = {
  neutral: "text-muted-foreground",
  info: "text-info",
  success: "text-success",
  warning: "text-warning",
  destructive: "text-destructive",
};

// AccountsGroup lists All accounts, then each of accounts with its inbox's
// unread count and status, its labels nested under it. place is the open
// place, if any, and filter its account filter; pathname is the address.
// Choosing an account keeps the open view, or a label of that account; a
// label of another becomes the Inbox, as does All accounts on a label,
// which opens only in its own account. failed says the accounts could not
// be read, which it says with Try again, onRetry, pending while retrying;
// the rail says it with one icon, which tries again. New label follows the
// accounts while any account's labels can change, and each such account's
// labels have a menu; both ask for what the member chose through onTask.
function AccountsGroup({
  accounts,
  failed,
  retrying,
  onRetry,
  counts,
  place,
  filter,
  pathname,
  onPress,
  onTask,
}: {
  accounts: readonly ListedAccount[];
  failed: boolean;
  retrying: boolean;
  onRetry: () => void;
  counts: readonly PlaceCount[];
  place: string | undefined;
  filter: number | undefined;
  pathname: string;
  onPress: () => void;
  onTask: (ask: LabelAsk | "new") => void;
}): ReactElement | null {
  if (failed) {
    return (
      <SidebarGroup>
        <SidebarGroupLabel>{accountsName}</SidebarGroupLabel>
        <div className="flex flex-col items-start gap-2 px-2 text-sm group-data-[collapsible=icon]:px-0">
          {/* Heard on the rail too, where the icon below shows instead. */}
          <p role="alert" className="group-data-[collapsible=icon]:sr-only">
            {accountsUnreadable}
          </p>
          <Button
            variant="outline"
            size="sm"
            className="group-data-[collapsible=icon]:hidden"
            isPending={retrying}
            onPress={onRetry}
          >
            {tryAgainText}
          </Button>
          <SidebarMenu className="hidden group-data-[collapsible=icon]:flex">
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip={`${accountsUnreadable} ${tryAgainText}.`}
                aria-label={tryAgainText}
                isPending={retrying}
                onPress={onRetry}
                className="text-destructive"
              >
                <CircleAlertIcon aria-hidden="true" />
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </div>
      </SidebarGroup>
    );
  }
  if (accounts.length === 0) return null;
  // What the place becomes under accountId's filter, or every account's.
  const placeFor = (accountId: number | undefined): string => {
    if (place === undefined) return "inbox";
    if (isViewPlace(place)) return place;
    if (accountId === undefined) return "inbox";
    const owner = counts.find((count) => count.place === place)?.accountId;
    return owner === accountId ? place : "inbox";
  };

  return (
    <SidebarGroup>
      <SidebarGroupLabel>{accountsName}</SidebarGroupLabel>
      <SidebarMenu>
        <NavItem
          href={linkOptions({
            to: "/mail/$place",
            params: { place: placeFor(undefined) },
            search: { account: undefined },
          })}
          name={allAccountsName}
          icon={LayersIcon}
          isActive={place !== undefined && filter === undefined}
          // A filter of the page, not a page: the place's own item is that.
          current="true"
          onPress={onPress}
        />
        {accounts.map((account) => (
          <AccountItem
            key={account.id}
            account={account}
            unread={unreadIn(counts, "inbox", account.id)}
            labels={labelTree(counts, account.id)}
            href={linkOptions({
              to: "/mail/$place",
              params: { place: placeFor(account.id) },
              search: { account: account.id },
            })}
            isActive={filter === account.id}
            pathname={pathname}
            onPress={onPress}
            onTask={account.allowedActions.includes("change-mail") ? onTask : undefined}
          />
        ))}
        {/* Only where some account's labels can change; the rail shows
            no labels, so it shows none either. */}
        {accounts.some((a) => a.allowedActions.includes("change-mail")) && (
          <SidebarMenuItem className="group-data-[collapsible=icon]:hidden">
            <SidebarMenuButton onPress={() => onTask("new")}>
              <PlusIcon aria-hidden="true" />
              <span>{newLabelName}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        )}
      </SidebarMenu>
    </SidebarGroup>
  );
}

// AccountItem is one account: its mark, name, status, and inbox's unread
// count, filtering every view to it, with its labels under it, folded
// behind the chevron at the row's end, each with its menu when onTask is
// given. A name cut short is said in full on hover or focus, before the
// status's details.
function AccountItem({
  account,
  unread,
  labels,
  href,
  isActive,
  pathname,
  onPress,
  onTask,
}: {
  account: ListedAccount;
  unread: number;
  labels: LabelNode[];
  href: AppHref;
  isActive: boolean;
  pathname: string;
  onPress: () => void;
  onTask: ((ask: LabelAsk) => void) | undefined;
}): ReactElement {
  const name = accountName(account);
  const status = accountStatus(account);
  const [nameRef, nameCut] = useOverflow(name);
  const unfolded = useUnfolded();
  const fold = accountFold(account.id);
  // The rail shows the mark alone, so its tooltip says the rest.
  const said = [name, unread > 0 && `${formatCount(unread)} ${unreadLabel}`, status?.words].filter(
    Boolean,
  );
  const detail = accountDetail(name, nameCut, status);
  const StatusIcon = status?.icon;
  const row = (
    <SidebarMenuButton
      href={href}
      isActive={isActive}
      // A filter of the page, not a page: the place's own item is that.
      current="true"
      // Where focus goes once a label's delete took the row that had it.
      data-account-row={account.id}
      tooltip={said.join(", ")}
      detail={detail || undefined}
      onPress={onPress}
      // Every row keeps the chevron's column, folding or not, so the
      // counts end at one edge (DESIGN.md "Sidebar"). Two lines fit a
      // touch's 44 px row with 4 px above and below.
      className={cn(
        "pr-8 pointer-coarse:pr-8",
        status && "pointer-coarse:py-1",
        unread > 0 && "font-semibold text-foreground",
      )}
    >
      {/* A box the size of an icon, so the mark keeps the icons' column. */}
      <span aria-hidden="true" className="flex size-4 shrink-0 items-center justify-center">
        <span className={cn("size-2 rounded-full", accountDot(account.color))} />
      </span>
      {status && StatusIcon ? (
        // With a mouse, the status's icon alone follows the name, so the
        // name keeps the room, and hover tells the words. A touch has no
        // hover, so the icon and words take a line under the name, which
        // keeps the row's width (DESIGN.md "Sidebar"). Screen readers
        // always hear the words.
        <span className="flex min-w-0 flex-1 items-center gap-2 pointer-coarse:flex-col pointer-coarse:items-start pointer-coarse:gap-0">
          <span
            ref={nameRef}
            className="max-w-full truncate"
            translate={account.name === null ? "no" : undefined}
          >
            {name}
          </span>
          <span
            className={cn(
              "ml-auto flex shrink-0 items-center gap-1 group-data-[collapsible=icon]:hidden pointer-coarse:ml-0 pointer-coarse:text-xs",
              toneText[status.tone],
            )}
          >
            <StatusIcon aria-hidden="true" />
            <span className="sr-only pointer-coarse:not-sr-only">{status.words}</span>
          </span>
        </span>
      ) : (
        <span ref={nameRef} translate={account.name === null ? "no" : undefined}>
          {name}
        </span>
      )}
      {unread > 0 && (
        <SidebarMenuBadge className={cn(status && "ml-0")}>
          {formatCount(unread)}
          <span className="sr-only"> {unreadLabel}</span>
        </SidebarMenuBadge>
      )}
    </SidebarMenuButton>
  );

  return (
    <SidebarMenuItem>
      {labels.length === 0 ? (
        row
      ) : (
        <Disclosure
          isExpanded={unfolded.has(fold)}
          onExpandedChange={(open) => setUnfolded(fold, open)}
        >
          {row}
          <FoldButton name={accountFoldName(name)} isExpanded={unfolded.has(fold)} />
          <DisclosurePanel>
            <LabelList
              labels={labels}
              accountId={account.id}
              pathname={pathname}
              onPress={onPress}
              onTask={onTask}
            />
          </DisclosurePanel>
        </Disclosure>
      )}
    </SidebarMenuItem>
  );
}

// FoldButton is the chevron, called name, at a row's end that folds or
// unfolds the labels under the row; Disclosure gives it its expanded
// state. className places it, e.g. a nested one back in its column.
function FoldButton({
  name,
  isExpanded,
  className,
}: {
  name: string;
  isExpanded: boolean;
  className?: string;
}): ReactElement {
  return (
    <SidebarMenuAction slot="trigger" aria-label={name} className={className}>
      <ChevronRightIcon
        aria-hidden="true"
        className={cn(
          "transition-transform duration-150 ease-standard motion-reduce:transition-none",
          isExpanded && "rotate-90",
        )}
      />
    </SidebarMenuAction>
  );
}

// LabelList lists labels, each with its unread count and its own labels
// under it, nested is true for a list under a label. A label opens in its
// account, accountId, its filter (DESIGN.md "Sidebar"); onTask, when given,
// gives each its menu.
function LabelList({
  labels,
  accountId,
  pathname,
  onPress,
  onTask,
  nested = false,
}: {
  labels: LabelNode[];
  accountId: number;
  pathname: string;
  onPress: () => void;
  onTask: ((ask: LabelAsk) => void) | undefined;
  nested?: boolean;
}): ReactElement {
  return (
    // No right margin, so a label's count ends in the accounts' column. A
    // level under a label steps in 12 px, not the first level's 24, so a
    // 240 px sidebar keeps room for a third level's names, and drops the
    // first level's 1 px shift, which would add up level by level.
    <SidebarMenuSub className={cn("mr-0 pr-0", nested && "ml-2 translate-x-0 pl-1")}>
      {labels.map((label) => (
        <LabelItem
          key={label.place}
          label={label}
          accountId={accountId}
          pathname={pathname}
          onPress={onPress}
          onTask={onTask}
        />
      ))}
    </SidebarMenuSub>
  );
}

// LabelItem is one label: its name and unread count, opening it, with the
// labels under it folded behind the chevron at the row's end, and its menu
// of Edit and Delete when onTask is given (LabelActionsMenu). A name cut
// short is said in full, with its parents', on hover or focus.
//
// DESIGN: no tag icon: every label would show the same one, and its place
// under its account already says what it is; the name gets the room.
function LabelItem({
  label,
  accountId,
  pathname,
  onPress,
  onTask,
}: {
  label: LabelNode;
  accountId: number;
  pathname: string;
  onPress: () => void;
  onTask: ((ask: LabelAsk) => void) | undefined;
}): ReactElement {
  const [nameRef, nameCut] = useOverflow(label.name);
  const unfolded = useUnfolded();
  const fold = labelFold(label.place);
  const parent = label.children.length > 0;
  const row = (
    <SidebarMenuSubButton
      href={linkOptions({
        to: "/mail/$place",
        params: { place: label.place },
        search: { account: accountId },
      })}
      isActive={within(pathname, `/mail/${label.place}`)}
      detail={nameCut ? label.path : undefined}
      onPress={onPress}
      // Every row keeps the chevron's column, as an account's does; the
      // menu's too while its ⋮ shows, so a long name ends before it, and
      // always on a touch, where the ⋮ always shows (LabelActionsMenu).
      className={cn(
        "pr-8",
        onTask &&
          "group-has-aria-expanded/label-row:pr-14 group-has-data-focus-visible/label-row:pr-14 pointer-coarse:pr-14 pointer-fine:group-hover/label-row:pr-14",
        label.unread > 0 && "font-semibold text-foreground",
      )}
    >
      <span ref={nameRef}>{label.name}</span>
      {label.unread > 0 && (
        <SidebarMenuBadge
          // The menu's ⋮ takes its place while it shows (LabelActionsMenu).
          className={cn(
            onTask &&
              "pointer-fine:group-hover/label-row:invisible pointer-fine:group-has-aria-expanded/label-row:invisible pointer-fine:group-has-data-focus-visible/label-row:invisible",
          )}
        >
          {formatCount(label.unread)}
          <span className="sr-only"> {unreadLabel}</span>
        </SidebarMenuBadge>
      )}
    </SidebarMenuSubButton>
  );
  // The row and its menu, apart from the labels under it, so pointing at
  // one of those shows only its own menu.
  const withMenu = onTask ? (
    <div className="group/label-row relative">
      {row}
      <LabelActionsMenu path={label.path} place={label.place} onTask={onTask} />
    </div>
  ) : (
    row
  );

  return (
    <SidebarMenuSubItem>
      {parent ? (
        <Disclosure
          isExpanded={unfolded.has(fold)}
          onExpandedChange={(open) => setUnfolded(fold, open)}
        >
          {withMenu}
          <FoldButton
            name={labelFoldName(label.path)}
            isExpanded={unfolded.has(fold)}
            // Back by the 1 px the labels' list shifts, in the accounts'. On
            // a touch, its target stops 1 px past its edge (2 px, less that
            // shift), where the ⋮'s begins, so a tap between them opens
            // what it lands nearest.
            className={cn("-translate-x-px", onTask && "pointer-coarse:after:-left-px")}
          />
          <DisclosurePanel>
            <LabelList
              labels={label.children}
              accountId={accountId}
              pathname={pathname}
              onPress={onPress}
              onTask={onTask}
              nested
            />
          </DisclosurePanel>
        </Disclosure>
      ) : (
        withMenu
      )}
    </SidebarMenuSubItem>
  );
}

// useUnfoldOpenPlace unfolds the way to the place the page shows, place
// filtered to the account filter: that account, and a label's account and
// parent labels, so the open place is never folded away. It runs once
// counts are read and again each time the place or filter changes, so
// what the member folds afterwards stays folded.
function useUnfoldOpenPlace(
  place: string | undefined,
  filter: number | undefined,
  counts: readonly PlaceCount[],
): void {
  const done = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (counts.length === 0) return;
    const at = `${place} ${filter}`;
    if (done.current === at) return;
    done.current = at;
    const keys: FoldKey[] = [];
    if (filter !== undefined) keys.push(accountFold(filter));
    const byPlace = new Map(counts.map((count) => [count.place, count]));
    const label = place === undefined || isViewPlace(place) ? undefined : byPlace.get(place);
    if (label) {
      keys.push(accountFold(label.accountId));
      const seen = new Set<string>();
      // A loop could come only from a server fault; it ends the walk.
      for (let parent = label.parent; parent && !seen.has(parent);) {
        seen.add(parent);
        keys.push(labelFold(parent));
        parent = byPlace.get(parent)?.parent ?? null;
      }
    }
    unfold(keys);
  }, [place, filter, counts]);
}

// useScrollToOpenPlace scrolls the open place's row in box into view, once
// for each place and filter, at, as soon as the row shows: e.g. when the
// drawer opens on a label far down, or once its path unfolds. counts and
// unfolded are what can make the row show, so each change looks again.
function useScrollToOpenPlace(
  box: RefObject<HTMLElement | null>,
  at: string,
  counts: readonly PlaceCount[],
  unfolded: ReadonlySet<string>,
): void {
  const done = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (done.current === at) return;
    const row = box.current?.querySelector<HTMLElement>('[aria-current="page"]');
    if (!row?.checkVisibility()) return;
    done.current = at;
    // At once, not smoothly, so reduced motion needs nothing else.
    row.scrollIntoView({ block: "nearest" });
  }, [box, at, counts, unfolded]);
}
