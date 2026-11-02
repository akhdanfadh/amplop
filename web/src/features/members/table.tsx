import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTable } from "@tanstack/react-table";
import {
  getListMembersQueryKey,
  useDeactivateMember,
  useReactivateMember,
} from "@/api/generated/inbok";
import type { Member, ResponseChangedMember, ResponseListedMembers } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { actionId, columns, features } from "./columns";
import { changedMessage, changeErrorMessage, type MemberChange } from "./text";

const unauthorized = 401;
const forbidden = 403;
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";

// MemberTable lists every account and deactivates or reactivates members.
// yourEmail is the signed-in account's address, whose row has no actions;
// administrators have none either. fetching marks the list as being reread.
// onAccessError receives a write's 401 or 403.
export function MemberTable({
  members,
  yourEmail,
  fetching,
  onAccessError,
}: {
  members: Member[];
  yourEmail: string;
  fetching: boolean;
  onAccessError: (err: ApiError) => void;
}): ReactElement {
  const client = useQueryClient();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // The member a write last acted on; focus returns to its row's button.
  const restoreFocus = useRef<number | null>(null);
  // The change and member the dialog asks about, kept while it closes so its
  // text stays.
  const [confirming, setConfirming] = useState<{ change: MemberChange; member: Member } | null>(
    null,
  );
  const [dialogOpen, setDialogOpen] = useState(false);
  // Set by the dialog's confirm button. The write starts once the dialog has
  // closed: started earlier, the dialog would give focus back to a button the
  // write has disabled or the reread has removed, and focus would be lost.
  const confirmed = useRef(false);
  const [changed, setChanged] = useState<{ change: MemberChange; email: string } | null>(null);
  const writeOptions = {
    mutation: {
      gcTime: 0,
      onMutate: prepareWrite,
      onSuccess: showChanged,
      onSettled: finishWrite,
      onError: (err: ApiError) => {
        // The page signs out or rereads access instead of a message here.
        if (isAccessError(err)) onAccessError(err);
      },
    },
  };
  const deactivate = useDeactivateMember(writeOptions);
  const reactivate = useReactivateMember(writeOptions);
  const pending = deactivate.isPending || reactivate.isPending;
  const table = useTable({
    features,
    columns,
    data: members,
    getRowId: (member) => String(member.id),
    meta: {
      yourEmail,
      // Variables outlive the write, so only a pending one is running.
      running: deactivate.isPending
        ? { change: "deactivate", id: deactivate.variables.id }
        : reactivate.isPending
          ? { change: "reactivate", id: reactivate.variables.id }
          : null,
      disabled: pending,
      onChoose: (change, member) => {
        confirmed.current = false;
        setConfirming({ change, member });
        setDialogOpen(true);
      },
    },
  });
  const write = changed?.change === "deactivate" ? deactivate : reactivate;
  const notice = changed && write.isSuccess ? changedMessage(changed.change, changed.email) : "";
  const failure =
    changed && write.error && !isAccessError(write.error)
      ? changeErrorMessage(write.error, changed.change, changed.email)
      : "";

  useEffect(() => {
    // A disabled button loses focus, so give it back once the list is reread.
    const id = restoreFocus.current;
    if (id === null || pending || fetching) return;
    restoreFocus.current = null;
    if (document.activeElement !== document.body) return;
    (document.getElementById(actionId(id)) ?? headingRef.current)?.focus();
  }, [pending, fetching]);

  // prepareWrite stops a list read, so it cannot overwrite the write's result.
  async function prepareWrite(): Promise<void> {
    await client.cancelQueries({ queryKey: getListMembersQueryKey(), exact: true });
  }

  // showChanged puts the member from result into the cached list.
  function showChanged(result: ResponseChangedMember): void {
    const saved = result.data.member;
    // The row is right at once; the reread only confirms it.
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

  // finishWrite rereads the list after any outcome.
  function finishWrite(): void {
    // Every outcome, including a lost response, is settled by the server's list.
    void client.invalidateQueries({ queryKey: getListMembersQueryKey() });
  }

  // start runs change for member unless another write is running.
  function start(change: MemberChange, member: Member): void {
    if (pending) return;
    deactivate.reset();
    reactivate.reset();
    restoreFocus.current = member.id;
    setChanged({ change, email: member.email });
    (change === "deactivate" ? deactivate : reactivate).mutate({ id: member.id });
  }

  return (
    // The scrolling table is the named region; naming the section too would repeat it.
    <section className="flex flex-col gap-6">
      <div className="flex max-w-prose flex-col gap-2">
        <h2 id="members-heading" ref={headingRef} tabIndex={-1} className="text-xl font-semibold">
          Member list
        </h2>
        <p>
          Deactivating a member signs them out and stops them from signing in. Reactivating them
          lets them sign in again as before. Administrators can&apos;t be deactivated.
        </p>
      </div>
      {/* Stay mounted so screen readers announce changed text. */}
      <output className={notice ? undefined : "sr-only"}>{notice}</output>
      {failure && <p role="alert">{failure}</p>}
      {failure && write.error?.requestId && (
        <p className="text-sm text-muted-foreground">
          Request reference: <code className="break-all">{write.error.requestId}</code>
        </p>
      )}
      {/* The page wraps long words anywhere; in the table that would squeeze columns to a letter. */}
      <Table
        className="wrap-normal"
        aria-labelledby="members-heading"
        aria-busy={fetching || pending}
        // The table scrolls sideways on a phone; focus lets the keyboard scroll it.
        containerProps={{
          tabIndex: 0,
          role: "region",
          "aria-labelledby": "members-heading",
          className: focusClass,
        }}
      >
        <TableHeader>
          {table.getHeaderGroups().map((group) => (
            <TableRow key={group.id}>
              {group.headers.map((header) => (
                <TableHead key={header.id} scope="col">
                  <table.FlexRender header={header} />
                </TableHead>
              ))}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.map((row) => (
            <TableRow key={row.id}>
              {row.getAllCells().map((cell) => (
                <TableCell key={cell.id} className={cell.column.columnDef.meta?.cellClassName}>
                  <table.FlexRender cell={cell} />
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <AlertDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onOpenChangeComplete={(open) => {
          if (open || !confirmed.current || !confirming) return;
          confirmed.current = false;
          start(confirming.change, confirming.member);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="wrap-anywhere">
              {confirming?.change === "reactivate" ? "Reactivate" : "Deactivate"}{" "}
              <span translate="no">{confirming?.member.email}</span>?
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirming?.change === "reactivate"
                ? "They can sign in again as before."
                : "They are signed out of every browser now and can't sign in until you reactivate them. Their account and sign-in details are kept."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className={`min-h-11 border-muted-foreground ${focusClass}`}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              type="button"
              variant={confirming?.change === "reactivate" ? "default" : "destructive"}
              className={`min-h-11 ${focusClass}`}
              disabled={pending}
              onClick={() => {
                confirmed.current = true;
                setDialogOpen(false);
              }}
            >
              {confirming?.change === "reactivate" ? "Reactivate" : "Deactivate"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

// isAccessError reports whether err is a 401 or 403.
function isAccessError(err: ApiError): boolean {
  return err.status === unauthorized || err.status === forbidden;
}
