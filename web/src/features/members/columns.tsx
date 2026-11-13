import type { ReactElement } from "react";
import { BanIcon, CircleCheckIcon, UserRoundIcon } from "lucide-react";
import { createColumnHelper, metaHelper, tableFeatures } from "@tanstack/react-table";
import type { Member } from "@/api/generated/model";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { canChange, formatDate, roleName, type MemberChange } from "./text";

const actionLabels: Record<MemberChange, string> = {
  deactivate: "Deactivate",
  reactivate: "Reactivate",
};
const runningLabels: Record<MemberChange, string> = {
  deactivate: "Deactivating...",
  reactivate: "Reactivating...",
};

// actionId returns the id of the row button that changes member id.
export function actionId(id: number): string {
  return `member-${id}-action`;
}

// MemberTableMeta is what the cells read from the table that renders them.
export type MemberTableMeta = {
  yourEmail: string;
  // The write that is running and the member it acts on, or null. The row may
  // already show the result, so the write, not the row, names the action.
  running: { change: MemberChange; id: number } | null;
  disabled: boolean;
  // Asks to confirm change for member.
  onChoose: (change: MemberChange, member: Member) => void;
};

// features are the table features the member list uses, with its meta types.
export const features = tableFeatures({
  tableMeta: metaHelper<MemberTableMeta>(),
  columnMeta: metaHelper<{ cellClassName?: string }>(),
});
const column = createColumnHelper<typeof features, Member>();

// columns are the member list's columns, in display order.
export const columns = column.columns([
  column.accessor("email", {
    header: "Email",
    meta: { cellClassName: "max-w-64 min-w-48 whitespace-normal" },
    cell: ({ getValue, table }) => (
      <EmailCell email={getValue()} you={getValue() === table.options.meta?.yourEmail} />
    ),
  }),
  // Status and its action come right after the address, so a phone shows
  // them before the table scrolls sideways.
  column.accessor("deactivatedAt", {
    id: "status",
    header: "Status",
    cell: ({ getValue }) => <StatusCell deactivatedAt={getValue()} />,
  }),
  column.display({
    id: "action",
    header: () => <span className="sr-only">Actions</span>,
    cell: ({ row, table }) => {
      const meta = table.options.meta;
      return meta ? <ActionCell member={row.original} meta={meta} /> : null;
    },
  }),
  column.accessor("role", { header: "Role", cell: ({ getValue }) => roleName(getValue()) }),
  column.accessor("joinedAt", {
    header: "Joined",
    cell: ({ getValue }) => <DateCell iso={getValue()} />,
  }),
  column.accessor("emailVerified", {
    header: "Verified",
    cell: ({ getValue }) => (getValue() ? "Yes" : "No"),
  }),
  column.accessor("totpEnabled", {
    header: "Two-factor",
    cell: ({ getValue }) => (getValue() ? "On" : "Off"),
  }),
]);

// EmailCell shows an address, marked "You" when it is the signed-in account's.
function EmailCell({ email, you }: { email: string; you: boolean }): ReactElement {
  return (
    <span className="flex flex-wrap items-center gap-2">
      <span className="wrap-anywhere" translate="no">
        {email}
      </span>
      {you && (
        <StatusBadge tone="neutral" icon={UserRoundIcon}>
          You
        </StatusBadge>
      )}
    </span>
  );
}

// StatusCell shows whether an account is active, or since when it is deactivated.
function StatusCell({ deactivatedAt }: { deactivatedAt: string | null }): ReactElement {
  if (deactivatedAt === null)
    return (
      <StatusBadge tone="success" icon={CircleCheckIcon}>
        Active
      </StatusBadge>
    );
  return (
    <span className="flex flex-col items-start gap-1">
      <StatusBadge tone="neutral" icon={BanIcon}>
        Deactivated
      </StatusBadge>
      <DateCell iso={deactivatedAt} className="text-xs text-muted-foreground" />
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

// ActionCell is the button that deactivates or reactivates member, or nothing
// when canChange refuses it.
function ActionCell({
  member,
  meta,
}: {
  member: Member;
  meta: MemberTableMeta;
}): ReactElement | null {
  if (!canChange(member, meta.yourEmail)) return null;
  const change: MemberChange = member.deactivatedAt === null ? "deactivate" : "reactivate";
  const running = meta.running?.id === member.id ? meta.running.change : null;

  return (
    <Button
      id={actionId(member.id)}
      type="button"
      variant="outline"
      // The button whose write runs stays focusable as pending, so focus
      // has a place when the dialog closes; the rest are disabled.
      isDisabled={meta.disabled && running === null}
      isPending={running !== null}
      onPress={() => meta.onChoose(change, member)}
    >
      {running ? runningLabels[running] : actionLabels[change]}
      <span className="sr-only"> {member.email}</span>
    </Button>
  );
}
