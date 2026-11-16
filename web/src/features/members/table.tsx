import { useEffect, useRef, useState, type ReactElement, type RefObject } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTable } from "@tanstack/react-table";
import { cn } from "@/lib/cn";
import {
  getListMembersQueryKey,
  useDeactivateMember,
  useReactivateMember,
} from "@/api/generated/inbok";
import type { Member, ResponseChangedMember, ResponseListedMembers } from "@/api/generated/model";
import { ConfirmDialog, type Failure } from "@/components/confirm-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Address } from "@/components/address";
import { columns, features } from "./columns";
import {
  actionWords,
  listTitle,
  changedMessage,
  changeErrorMessage,
  type MemberAction,
} from "./text";

const unauthorized = 401;
// The fades' size, Tailwind's 8 (w-8, h-8).
const fadePx = 32;

// The header stays at the top of the scroll box with its own bottom edge: a
// table's borders belong to its rows, which scroll away under it.
const stickyHead = "sticky top-0 z-20 bg-background shadow-[inset_0_-1px_0_var(--color-border)]";

// MemberTable lists every account and deactivates or reactivates members,
// each change confirmed and held in a dialog until the server answers.
// yourEmail is the signed-in account's address, marked You. The table
// scrolls inside its own box, which fills the height its page leaves
// (SettingsPage "fill").
export function MemberTable({
  members,
  yourEmail,
}: {
  members: Member[];
  yourEmail: string;
}): ReactElement {
  const client = useQueryClient();
  // The change the dialog asks about, kept while it closes so its words stay.
  const [asked, setAsked] = useState<{ action: MemberAction; member: Member }>();
  const [open, setOpen] = useState(false);
  // Read by screen readers once a change is saved; the row shows it to sight.
  const [announcement, setAnnouncement] = useState("");
  const boxRef = useRef<HTMLDivElement>(null);
  const { scrollable, moreRight, moreBelow } = useScrollBox(boxRef);
  const writeOptions = {
    mutation: {
      gcTime: 0,
      // A list read that started before the write could land after it.
      onMutate: () => client.cancelQueries({ queryKey: getListMembersQueryKey(), exact: true }),
      onSuccess: showChanged,
      // Every outcome, a lost answer included, is settled by the server's list.
      onSettled: () => void client.invalidateQueries({ queryKey: getListMembersQueryKey() }),
    },
  };
  const deactivate = useDeactivateMember(writeOptions);
  const reactivate = useReactivateMember(writeOptions);
  const write = asked?.action === "reactivate" ? reactivate : deactivate;
  const table = useTable({
    features,
    columns,
    data: members,
    getRowId: (member) => String(member.id),
    meta: { yourEmail, onChoose: choose },
  });
  const failure: Failure | undefined =
    asked && write.error && write.error.status !== unauthorized
      ? {
          message: changeErrorMessage(write.error, asked.action, asked.member.email),
          requestId: write.error.requestId,
        }
      : undefined;
  const words = asked ? actionWords(asked.action) : actionWords("deactivate");

  // choose opens the dialog that confirms action for member.
  function choose(action: MemberAction, member: Member): void {
    deactivate.reset();
    reactivate.reset();
    setAsked({ action, member });
    setOpen(true);
  }

  // confirm sends the change the dialog asks about.
  function confirm(): void {
    if (!asked || write.isPending) return;
    setAnnouncement("");
    write.mutate(
      { id: asked.member.id },
      {
        onSuccess: () => {
          setOpen(false);
          setAnnouncement(changedMessage(asked.action, asked.member.email));
        },
      },
    );
  }

  // showChanged puts the member from result into the cached list, so its
  // row is right at once; the reread only confirms it.
  function showChanged(result: ResponseChangedMember): void {
    const saved = result.data.member;
    client.setQueryData<ResponseListedMembers>(getListMembersQueryKey(), (list) =>
      list
        ? {
            ...list,
            data: {
              ...list.data,
              members: list.data.members.map((m) => (m.id === saved.id ? saved : m)),
            },
          }
        : list,
    );
  }

  return (
    <>
      <output aria-atomic="true" className="sr-only">
        {announcement}
      </output>
      {/* One scroll box both ways, so the header and the first column stick
          to it, not to a box around it (MDN, position: sticky). The frame
          around it holds the fade, which stays put while the box scrolls. */}
      <div className="relative flex min-h-48 flex-1 flex-col">
        <Table
          className="wrap-normal"
          containerProps={{
            ref: boxRef,
            // Focusable while it scrolls, so the keyboard can scroll it
            // (DESIGN.md "Settings and signed-out pages"); a list that fits
            // needs no extra Tab stop.
            tabIndex: scrollable ? 0 : undefined,
            // A region only while it scrolls, named for what scrolls; the
            // section is already the "Accounts" region.
            role: scrollable ? "region" : undefined,
            "aria-label": scrollable ? listTitle : undefined,
            className:
              // Scrolling to a focused button stops clear of the sticky header
              // (40 px) and the bottom fade (32 px), not under them.
              "group/table @container min-h-0 flex-1 scroll-pt-10 scroll-pb-8 overflow-auto overscroll-y-contain focus-visible:focus-ring",
          }}
        >
          <TableHeader className="[&_tr]:border-0">
            {table.getHeaderGroups().map((group) => (
              <TableRow key={group.id} className="hover:bg-transparent">
                {group.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    scope="col"
                    className={cn(stickyHead, header.column.columnDef.meta?.headClassName)}
                  >
                    <table.FlexRender header={header} />
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.map((row) => (
              <TableRow key={row.id} className="group/row hover:bg-muted">
                {row.getAllCells().map((cell) => {
                  const meta = cell.column.columnDef.meta;
                  return meta?.rowHeader ? (
                    <TableHead
                      key={cell.id}
                      scope="row"
                      className={cn("h-auto p-2 font-normal", meta.cellClassName)}
                    >
                      <table.FlexRender cell={cell} />
                    </TableHead>
                  ) : (
                    <TableCell key={cell.id} className={meta?.cellClassName}>
                      <table.FlexRender cell={cell} />
                    </TableCell>
                  );
                })}
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {/* More rows lie below, e.g. in a short window. */}
        {moreBelow && (
          <div
            aria-hidden="true"
            data-slot="more-below"
            className="pointer-events-none absolute inset-x-0 bottom-0 z-40 h-8 bg-linear-to-t from-background"
          />
        )}
        {/* More columns lie to the right, e.g. the row's action on a phone. */}
        {moreRight && (
          <div
            aria-hidden="true"
            data-slot="more-right"
            className="pointer-events-none absolute inset-y-0 right-0 z-40 w-8 bg-linear-to-l from-background"
          />
        )}
      </div>
      <ConfirmDialog
        isOpen={open}
        onOpenChange={setOpen}
        title={
          asked && (
            <>
              {/* Heard as one sentence; the address is split only for the eye. */}
              <span className="sr-only">{`${words.action} ${asked.member.email}?`}</span>
              <span aria-hidden="true">
                {words.action} <Address email={asked.member.email} />?
              </span>
            </>
          )
        }
        description={words.consequence}
        action={words.action}
        working={words.working}
        tone={asked?.action === "deactivate" ? "destructive" : "default"}
        pending={write.isPending}
        failure={failure}
        onConfirm={confirm}
      />
    </>
  );
}

// useScrollBox follows the scroll box ref holds as it scrolls and as it or
// its table changes size: whether it scrolls at all, and whether content
// lies beyond its right or bottom edge. It marks the box data-scrolled while its
// columns are scrolled sideways, which shows the first column's edge.
function useScrollBox(ref: RefObject<HTMLDivElement | null>): {
  scrollable: boolean;
  moreRight: boolean;
  moreBelow: boolean;
} {
  const [state, setState] = useState({
    scrollable: false,
    moreRight: false,
    moreBelow: false,
  });

  useEffect(() => {
    const box = ref.current;
    // The box renders with the table, so it is there by now.
    if (!box) return () => {};
    const measure = () => {
      const scrollable = box.scrollWidth > box.clientWidth || box.scrollHeight > box.clientHeight;
      // Only more than a fade's width: a sliver left would put the whole
      // fade over every row's button, e.g. at 200% zoom.
      const moreRight = box.scrollWidth - box.clientWidth - box.scrollLeft > fadePx;
      const moreBelow = box.scrollHeight - box.clientHeight - box.scrollTop > fadePx;
      box.toggleAttribute("data-scrolled", box.scrollLeft > 0);
      setState((last) =>
        last.scrollable === scrollable &&
        last.moreRight === moreRight &&
        last.moreBelow === moreBelow
          ? last
          : { scrollable, moreRight, moreBelow },
      );
    };
    measure();
    const resized = new ResizeObserver(measure);
    resized.observe(box);
    if (box.firstElementChild) resized.observe(box.firstElementChild);
    box.addEventListener("scroll", measure, { passive: true });
    return () => {
      resized.disconnect();
      box.removeEventListener("scroll", measure);
    };
  }, [ref]);

  return state;
}
