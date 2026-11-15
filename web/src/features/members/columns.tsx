import type { ReactElement } from "react";
import { BanIcon, CircleDashedIcon, ShieldUserIcon, UserRoundIcon } from "lucide-react";
import { createColumnHelper, metaHelper, tableFeatures } from "@tanstack/react-table";
import type { Member } from "@/api/generated/model";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { actionWords, formatDate, offeredAction, type MemberAction } from "./text";

// MemberTableMeta is what the cells read from the table that renders them.
type MemberTableMeta = {
  yourEmail: string;
  // Asks to confirm action for member.
  onChoose: (action: MemberAction, member: Member) => void;
};

// The first column stays at the left while the rest scroll sideways under
// it; a hairline marks its edge only while they do (data-scrolled on the
// scroll box, the group "table"). Its background follows the row's hover,
// or scrolled cells would show through.
const pinned =
  "sticky left-0 bg-background group-hover/row:bg-muted group-data-scrolled/table:shadow-[inset_-1px_0_0_var(--color-border)]";

// features are the table features the member list uses, with its meta types.
export const features = tableFeatures({
  tableMeta: metaHelper<MemberTableMeta>(),
  columnMeta: metaHelper<{ headClassName?: string; cellClassName?: string; rowHeader?: true }>(),
});
const column = createColumnHelper<typeof features, Member>();

// columns are the member list's columns, in display order: who, then the
// status the page's action changes, then the rest, then the action.
export const columns = column.columns([
  column.accessor("email", {
    header: "Member",
    meta: {
      rowHeader: true,
      // The header is pinned both ways; its edge shows on both sides while
      // the columns scroll under it.
      headClassName:
        "left-0 z-30 group-data-scrolled/table:shadow-[inset_-1px_-1px_0_var(--color-border)]",
      cellClassName: `${pinned} z-10 max-w-64 min-w-36 whitespace-normal @md:min-w-48`,
    },
    cell: ({ row, table }) => (
      <MemberCell
        member={row.original}
        you={row.original.email === table.options.meta?.yourEmail}
      />
    ),
  }),
  column.accessor("deactivatedAt", {
    id: "status",
    header: "Status",
    cell: ({ getValue }) => <StatusCell deactivatedAt={getValue()} />,
  }),
  column.accessor("totpEnabled", {
    header: "Two-factor",
    cell: ({ getValue }) => (getValue() ? "On" : "Off"),
  }),
  column.accessor("joinedAt", {
    header: "Joined",
    meta: { cellClassName: "tabular-nums" },
    cell: ({ getValue }) => <DateCell iso={getValue()} />,
  }),
  column.display({
    id: "action",
    header: () => <span className="sr-only">Actions</span>,
    meta: { cellClassName: "text-right" },
    cell: ({ row, table }) => {
      const meta = table.options.meta;
      return meta ? <ActionCell member={row.original} onChoose={meta.onChoose} /> : null;
    },
  }),
]);

// MemberCell shows an account's address and, as badges, only what differs
// from an ordinary member: you, an administrator, an unverified address.
function MemberCell({ member, you }: { member: Member; you: boolean }): ReactElement {
  const admin = member.role === "admin";
  return (
    <span className="flex flex-col items-start gap-1">
      <Address email={member.email} />
      {(you || admin || !member.emailVerified) && (
        <span className="flex flex-wrap gap-1">
          {you && (
            <StatusBadge tone="neutral" icon={UserRoundIcon}>
              You
            </StatusBadge>
          )}
          {admin && (
            <StatusBadge tone="neutral" icon={ShieldUserIcon}>
              Admin
            </StatusBadge>
          )}
          {!member.emailVerified && (
            <StatusBadge tone="neutral" icon={CircleDashedIcon}>
              Not verified
            </StatusBadge>
          )}
        </span>
      )}
    </span>
  );
}

// Address shows email, wrapping at its seams: first before the "@", so the
// local part and the domain each stay whole while they fit, then before a
// dot. A part too long for its line still breaks anywhere, e.g. a
// 64-character local part (RFC 5321), rather than spilling under the next
// column. Screen readers get the address whole.
export function Address({ email }: { email: string }): ReactElement {
  const at = email.lastIndexOf("@");
  const split = at < 0 ? email.length : at;
  // A whole half moves to the next line before it breaks inside.
  const half = "inline-block max-w-full";
  return (
    <span translate="no">
      {/* Chrome joins the parts below with spaces when it names a dialog or
          a cell, so the parts are for the eye and this is for the ear. */}
      <span className="sr-only">{email}</span>
      <span aria-hidden="true" className="wrap-anywhere">
        <span className={half}>{dotted(email.slice(0, split))}</span>
        <wbr />
        <span className={half}>{dotted(email.slice(split))}</span>
      </span>
    </span>
  );
}

// dotted returns text with a break opportunity before each dot.
function dotted(text: string): ReactElement[] {
  const parts: ReactElement[] = [];
  let offset = 0;
  for (const part of text.split(/(?=\.)/)) {
    if (offset > 0) parts.push(<wbr key={`break-${offset}`} />);
    parts.push(<span key={offset}>{part}</span>);
    offset += part.length;
  }
  return parts;
}

// StatusCell shows Active quietly, as most rows are, and a deactivated
// account as a badge with its date.
function StatusCell({ deactivatedAt }: { deactivatedAt: string | null }): ReactElement {
  if (deactivatedAt === null) return <span className="text-muted-foreground">Active</span>;
  return (
    <span className="flex flex-col items-start gap-1">
      <StatusBadge tone="neutral" icon={BanIcon}>
        Deactivated
      </StatusBadge>
      <DateCell iso={deactivatedAt} className="text-xs text-muted-foreground tabular-nums" />
    </span>
  );
}

// DateCell shows the calendar date of iso.
function DateCell({ iso, className }: { iso: string; className?: string }): ReactElement {
  return (
    <time dateTime={iso} className={className}>
      {formatDate(iso)}
    </time>
  );
}

// ActionCell is the button for the action member's row offers, or nothing.
function ActionCell({
  member,
  onChoose,
}: {
  member: Member;
  onChoose: (action: MemberAction, member: Member) => void;
}): ReactElement | null {
  const action = offeredAction(member);
  if (!action) return null;
  return (
    <Button type="button" variant="outline" onPress={() => onChoose(action, member)}>
      {actionWords(action).action}
      <span className="sr-only"> {member.email}</span>
    </Button>
  );
}
