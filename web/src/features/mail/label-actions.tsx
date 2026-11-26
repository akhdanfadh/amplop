import { useEffect, useId, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { EllipsisVerticalIcon, PencilIcon, Trash2Icon } from "lucide-react";
import { getListPlacesQueryKey, listPlaces, useDeleteLabel } from "@/api/generated/amplop";
import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { ConfirmDialog, type Failure } from "@/components/confirm-dialog";
import { DropdownMenu, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { SidebarMenuAction } from "@/components/ui/sidebar";
import { cn } from "@/lib/cn";
import { refreshNewest } from "./conversations";
import type { Label } from "./labels";
import { say } from "./undo";
import {
  alsoDeleted,
  andMore,
  deleteLabelAction,
  deleteLabelBody,
  deleteLabelItem,
  deleteLabelTitle,
  deletingText,
  editItem,
  halfDeleted,
  labelActionsName,
  labelDeleted,
  opensPicker,
} from "./text";

// LabelAsk is what the member asked to do to a label from its menu: edit
// it or delete it, by its place.
export type LabelAsk = { kind: "edit" | "delete"; place: string };

// LabelActionsMenu is a sidebar label's ⋮, named for the label at path,
// opening its menu: Edit… and Delete label…, which run onTask. It sits in
// the row's count's place, shown on hover, while focus is in the row, and
// while its menu is open; a touch has no hover, so there it always shows,
// after the count (DESIGN.md "Sidebar").
//
// DESIGN: it covers the count rather than taking a column of its own, so a
// 240 px sidebar keeps room for a third level's names. The count comes back
// once the pointer or focus leaves; the label's page shows it too (WCAG
// 1.4.13 asks only that content shown on hover stay; this hides some, a
// grey area recorded in DESIGN.md).
export function LabelActionsMenu({
  path,
  place,
  onTask,
}: {
  path: string;
  place: string;
  onTask: (ask: LabelAsk) => void;
}): ReactElement {
  return (
    <DropdownMenuTrigger>
      <SidebarMenuAction
        aria-label={labelActionsName(path)}
        // Before the chevron's column; on a touch's 44 px row, centred, as
        // the chevron is.
        className={cn(
          // On a touch, its target stops 2 px past its edge, where the
          // chevron's begins, rather than under it.
          "right-8 pointer-coarse:after:-right-0.5",
          // Hidden until the row is pointed at or focused, or its menu is
          // open; the group's selectors outweigh the media query's.
          "group-hover/label-row:opacity-100 group-has-data-focus-visible/label-row:opacity-100 aria-expanded:opacity-100 pointer-fine:opacity-0",
        )}
      >
        <EllipsisVerticalIcon aria-hidden="true" />
      </SidebarMenuAction>
      <DropdownMenu placement="bottom end" className="w-auto">
        <DropdownMenuItem textValue={editItem} onAction={() => onTask({ kind: "edit", place })}>
          <PencilIcon aria-hidden="true" />
          {opensPicker(editItem)}
        </DropdownMenuItem>
        <DropdownMenuItem
          textValue={deleteLabelItem}
          variant="destructive"
          onAction={() => onTask({ kind: "delete", place })}
        >
          <Trash2Icon aria-hidden="true" />
          {opensPicker(deleteLabelItem)}
        </DropdownMenuItem>
      </DropdownMenu>
    </DropdownMenuTrigger>
  );
}

// How many of the labels inside one being deleted its dialog lists by
// name; the rest it counts.
const insideShown = 5;

// DeleteLabelDialog asks, while isOpen, before deleting label, of labels,
// the member's, and every label inside it (DESIGN.md "Sidebar"), then
// deletes them, closing with onClose. Their mail stays. Once the open
// label is gone, it or one it is inside deleted, even by a delete that
// then failed, its account's Inbox opens in its place, the conversation
// closed. Screen readers hear that it went. A delete that
// failed part way says so, the labels left listed, and pressing again
// finishes it; one already gone counts as done. fallbackFocus is where
// focus goes once the row that opened it has gone.
export function DeleteLabelDialog({
  isOpen,
  label,
  labels,
  onClose,
  fallbackFocus,
}: {
  isOpen: boolean;
  label: Label | undefined;
  labels: readonly Label[];
  onClose: () => void;
  fallbackFocus: () => HTMLElement | null;
}): ReactElement {
  const client = useQueryClient();
  const router = useRouter();
  // The labels it last asked to delete, to tell a failure part way.
  const [asked, setAsked] = useState<readonly string[]>([]);
  const remove = useDeleteLabel({
    mutation: {
      gcTime: 0,
      // Here, not with the call, so they run even if the sidebar closed.
      onSuccess: ({ data: { deleted } }) => settle(async (open) => deleted.includes(open)),
      // A refusal can come after Gmail deleted some, or all, of them, the
      // open one among them, or the answer was lost.
      onError: () => settle(async (open) => !(await stillListed(open))),
    },
  });

  // settle ends a delete, whatever its outcome: gone says whether the
  // open label, the place's page shows, is gone now.
  async function settle(gone: (open: string) => Promise<boolean>): Promise<void> {
    // 1. Off a deleted label's page, to its account's Inbox, before the
    // labels are read again, so the page never shows the label missing.
    const open = openPlace();
    if (label && open && inTree(label, open) && (await gone(open))) {
      await router.navigate({
        to: "/mail/$place",
        params: { place: "inbox" },
        search: { account: label.accountId },
        replace: true,
      });
    }
    // 2. Read the labels again.
    await reread();
  }

  // openPlace returns the place the place's page shows, if any.
  function openPlace(): string | undefined {
    const match = router.state.matches.find((m) => m.routeId === "/_app/_mail/mail/$place");
    return (match?.params as { place?: string } | undefined)?.place;
  }

  // inTree reports whether open is parent or a label inside it, among the
  // labels as they were read before the delete.
  function inTree(parent: Label, open: string): boolean {
    return labels.some(
      (l) =>
        l.place === open &&
        l.accountId === parent.accountId &&
        (l.place === parent.place || l.path.startsWith(`${parent.path}/`)),
    );
  }

  // reread reads the member's labels again, which the sidebar, the menus,
  // and the marks show, and the lists' rows, whose labels a delete changed.
  async function reread(): Promise<void> {
    void refreshNewest(client);
    await client.invalidateQueries({ queryKey: getListPlacesQueryKey() });
  }

  const present = new Set(labels.map((l) => l.place));
  // Gone after a failure: what the member asked for holds.
  const gone = label !== undefined && remove.isError && !present.has(label.place);
  const inside = label
    ? labels.filter((l) => l.accountId === label.accountId && l.path.startsWith(`${label.path}/`))
    : [];
  const failure = failureOf(remove.error, gone, asked, present);

  // close closes the dialog, saying a delete that went through.
  const close = (deleted: boolean) => {
    if (deleted && label) say(labelDeleted(label.name));
    setAsked([]);
    remove.reset();
    onClose();
  };
  // The latest close, for the effect below, which runs once per change.
  const latestClose = useRef(close);
  useEffect(() => {
    latestClose.current = close;
  });
  useEffect(() => {
    if (gone) latestClose.current(true);
  }, [gone]);

  return (
    <ConfirmDialog
      isOpen={isOpen && !gone}
      onOpenChange={(open) => {
        if (!open) close(false);
      }}
      title={label ? deleteLabelTitle(label.path) : ""}
      description={label ? deleteLabelBody(label.name, inside.length) : ""}
      action={label ? deleteLabelAction(label.name) : ""}
      working={deletingText}
      tone="destructive"
      pending={remove.isPending}
      failure={failure}
      onConfirm={() => {
        if (!label) return;
        setAsked([label.place, ...inside.map((l) => l.place)]);
        remove.mutate({ id: label.place }, { onSuccess: () => close(true) });
      }}
      fallbackFocus={fallbackFocus}
    >
      {label && inside.length > 0 && <InsideList parent={label} inside={inside} />}
    </ConfirmDialog>
  );
}

// stillListed reports whether List Places still has place, read without
// the cache, so the screen's labels change only once the page has left a
// deleted one. A read that fails counts as listed: the page then follows
// the next read (useFollowLabel).
async function stillListed(place: string): Promise<boolean> {
  try {
    const { data } = await listPlaces();
    return data.places.some((p) => p.place === place);
  } catch {
    return true;
  }
}

// failureOf returns what a delete's dialog says of err, the delete's
// failure, if any: nothing once the label is gone; that some labels were
// deleted when any it asked to delete, of asked, are no longer present;
// else why it failed.
function failureOf(
  err: ApiError | null,
  gone: boolean,
  asked: readonly string[],
  present: ReadonlySet<string>,
): Failure | undefined {
  if (!err || gone) return undefined;
  const requestId = err instanceof ApiError ? err.requestId : undefined;
  if (asked.some((place) => !present.has(place))) return { message: halfDeleted, requestId };
  return { message: userMessage(err), requestId };
}

// InsideList lists the labels inside parent that its delete takes too, by
// their paths under it, the first few by name, then how many more.
function InsideList({ parent, inside }: { parent: Label; inside: readonly Label[] }): ReactElement {
  const shown = inside.slice(0, insideShown);
  const id = useId();
  return (
    <div className="rounded-lg bg-muted px-4 py-3 text-sm">
      <p id={id} className="font-medium">
        {alsoDeleted}
      </p>
      <ul aria-labelledby={id} className="mt-1 list-disc pl-5">
        {shown.map((l) => (
          <li key={l.place} dir="auto" translate="no" className="wrap-anywhere">
            {l.path.slice(parent.path.length + 1)}
          </li>
        ))}
      </ul>
      {/* After the list, not in it, so it doesn't read as a label's name. */}
      {inside.length > shown.length && (
        <p className="mt-1">{andMore(inside.length - shown.length)}</p>
      )}
    </div>
  );
}
