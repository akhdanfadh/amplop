import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetRegistrationSettingsQueryKey,
  getGetSystemEmailQueryKey,
  useSaveRegistrationSettings,
} from "@/api/generated/inbok";
import type { RegistrationSettings } from "@/api/generated/model";
import { uncertainOutcome } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Field, FieldDescription } from "@/components/ui/field";
import { Switch } from "@/components/ui/switch";
import {
  confirmation,
  pendingMessage,
  saveErrorMessage,
  savedMessage,
  statusMessage,
  untestedMessage,
  type RegistrationChange,
} from "./text";

const unauthorized = 401;
const forbidden = 403;
const statusId = "registration-status";
const noteId = "registration-note";

// RegistrationSection says whether visitors can create accounts and opens or
// closes registration. settings is the last read and checkedAt when it
// arrived, in milliseconds; emailTestedAt is when the system email settings
// in use passed a test, absent when they have not; onAccessError receives
// every save error.
export function RegistrationSection({
  settings,
  checkedAt,
  emailTestedAt,
  onAccessError,
}: {
  settings: RegistrationSettings;
  checkedAt: number;
  emailTestedAt: string | undefined;
  onAccessError: (err: ApiError) => void;
}): ReactElement {
  const client = useQueryClient();
  const testedAt = useRef(emailTestedAt);
  // The change the dialog asks about stays after it closes, so its text does
  // not switch during the closing animation.
  const [dialog, setDialog] = useState<{ change: RegistrationChange; shown: boolean }>({
    change: "open",
    shown: false,
  });
  const save = useSaveRegistrationSettings({
    mutation: {
      // A read that started before the save could land after it and show the
      // old status.
      onMutate: () =>
        client.cancelQueries({ queryKey: getGetRegistrationSettingsQueryKey(), exact: true }),
      onSuccess: (result) => client.setQueryData(getGetRegistrationSettingsQueryKey(), result),
      onError: onAccessError,
      // Read again after every save: a read that started during it may hold
      // the old status, a refusal means this page is out of date, e.g. system
      // email changed in another tab, and a lost response may hide a change.
      // Lost access is the page's to handle.
      onSettled: (_, err) => {
        if (err?.status === unauthorized || err?.status === forbidden) return;
        void client.invalidateQueries({ queryKey: getGetRegistrationSettingsQueryKey() });
        if (err) void client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });
      },
    },
  });
  const { reset, isPending } = save;
  const { status } = settings;
  const change: RegistrationChange = status === "closed" ? "open" : "close";
  // Visitors get their link by email, so opening waits for a passing test.
  // The system email read says so as soon as a test ends.
  const blocked = change === "open" && !emailTestedAt;
  const asked: RegistrationChange = save.variables?.data.open ? "open" : "close";
  const notice = save.data?.data.status === status ? savedMessage(asked) : "";
  const error = save.error?.status === unauthorized ? null : save.error;
  // A change that may have been saved, with no read since, leaves the shown
  // status unproven.
  const unknown = Boolean(error && uncertainOutcome(error) && checkedAt < save.submittedAt);
  const asking = confirmation(dialog.change);

  // Saving or testing system email pauses or resumes registration on the
  // server, so read the status again when the tested time changes.
  useEffect(() => {
    if (testedAt.current === emailTestedAt) return;
    testedAt.current = emailTestedAt;
    // A new passing test makes the last save's notice or refusal out of
    // date. A cleared test keeps them: it may be what the refusal found. A
    // running save keeps its pending state and rereads when it ends.
    if (emailTestedAt && !isPending) reset();
    void client.invalidateQueries({ queryKey: getGetRegistrationSettingsQueryKey() });
  }, [emailTestedAt, client, reset, isPending]);

  // Saves the change the dialog asked about. Opening or closing twice changes
  // nothing, so a status that moved meanwhile does no harm.
  function confirm(): void {
    setDialog((last) => ({ ...last, shown: false }));
    if (isPending) return;
    save.mutate({ data: { open: dialog.change === "open" } });
  }

  return (
    <section className="flex flex-col gap-6" aria-labelledby="registration-heading">
      <div className="flex flex-col gap-2">
        <h2 id="registration-heading" className="text-xl font-semibold">
          Registration
        </h2>
        <p>
          When registration is open, anyone who can reach this inbok can create a member account:
          they give an email address, then choose a password from the link emailed to them. Each
          member adds storage and sync work to this server.
        </p>
      </div>

      <div className="flex flex-col gap-3">
        <Field>
          {/* Follows the saved status, never the click: a change waits for
              the dialog and the server. */}
          <Switch
            className={blocked ? "opacity-50" : undefined}
            isSelected={status !== "closed"}
            // Not disabled: a disabled switch leaves the tab order, and
            // keyboard users would never hear why it is blocked. Read-only
            // also keeps focus on the switch while it saves.
            isReadOnly={blocked || isPending}
            aria-describedby={blocked ? `${statusId} ${noteId}` : statusId}
            onChange={() => {
              if (blocked || isPending) return;
              setDialog({ change, shown: true });
            }}
          >
            Open registration
          </Switch>
          <FieldDescription id={statusId}>
            {isPending ? pendingMessage(asked) : statusMessage(unknown ? "unknown" : status)}
          </FieldDescription>
        </Field>
        {blocked && <p id={noteId}>{untestedMessage()}</p>}
      </div>

      {/* With no trigger, focus goes back to the switch, which had it:
          React Aria focuses the switch when its label is pressed too
          (switch.browser.test.tsx). */}
      <AlertDialog
        isOpen={dialog.shown}
        onOpenChange={(shown) => setDialog((last) => ({ ...last, shown }))}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{asking.title}</AlertDialogTitle>
          <AlertDialogDescription>{asking.description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction type="button" onPress={confirm}>
            {asking.action}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialog>

      {error && (
        <div className="flex flex-col gap-2" role="alert">
          <p>{saveErrorMessage(error, asked)}</p>
          {error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{error.requestId}</code>
            </p>
          )}
        </div>
      )}
      {/* Stay mounted so screen readers announce changed text. */}
      <output aria-atomic="true" className={notice ? undefined : "sr-only"}>
        {notice}
      </output>
    </section>
  );
}
