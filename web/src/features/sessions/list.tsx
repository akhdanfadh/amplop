import { useEffect, useRef, useState, type ReactNode } from "react";
import { MonitorIcon } from "lucide-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListSessionsQueryKey,
  useDeleteOtherSessions,
  useDeleteSession,
  useListSessions,
} from "@/api/generated/inbok";
import type { ListedSession } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import { clearAccountCache } from "@/features/auth/session-cache";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { StatusBadge } from "@/components/status-badge";
import { SettingsSection } from "@/features/settings/page";
import { Button } from "@/components/ui/button";
import {
  browserName,
  endedOthersMessage,
  endMessage,
  formatTime,
  lastActive,
  sessionsAbout,
  sessionsTitle,
  signOutLimit,
} from "./text";

const unauthorized = 401;
const buttonClass = "whitespace-normal";

// SessionList is Security's signed-in browsers section: each browser with a
// session, a way to sign each other one out, or all of them, then children,
// e.g. signing out of this one. locked disables the writes; paused stops
// reads while another write may replace or end this session's cookie;
// onWritingChange receives whether a write runs.
export function SessionList({
  locked,
  paused,
  onWritingChange,
  children,
}: {
  locked: boolean;
  // Another write may replace or end this session's cookie; a read sent now could see a 401.
  paused: boolean;
  onWritingChange: (pending: boolean) => void;
  children?: ReactNode;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const restoreFocus = useRef(false);
  const [endedName, setEndedName] = useState("");
  const [confirming, setConfirming] = useState(false);
  const end = useDeleteSession({ mutation: { onMutate: prepareWrite, onSettled: finishWrite } });
  const endOthers = useDeleteOtherSessions({
    mutation: { onMutate: prepareWrite, onSettled: finishWrite },
  });
  const pending = end.isPending || endOthers.isPending;
  const list = useListSessions({
    query: { enabled: !paused && !pending, networkMode: "always" },
  });
  const expired = [list.error, end.error, endOthers.error].some(
    (error) => error?.status === unauthorized,
  );
  const sessions = list.data?.data.sessions;
  const others = sessions?.filter((session) => !session.current).length ?? 0;
  const notice = end.isSuccess
    ? `${endedName} was signed out.`
    : endOthers.isSuccess
      ? endedOthersMessage(endOthers.data.data.ended)
      : "";

  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [expired, client, navigate]);

  useEffect(() => {
    // An ended row or the end-all button can leave the page with nothing focused.
    if (!restoreFocus.current || pending || list.isFetching) return;
    restoreFocus.current = false;
    if (document.activeElement === document.body) headingRef.current?.focus();
  }, [pending, list.isFetching]);

  async function prepareWrite(): Promise<void> {
    onWritingChange(true);
    restoreFocus.current = true;
    await client.cancelQueries({ queryKey: getListSessionsQueryKey(), exact: true });
  }

  function finishWrite(): void {
    onWritingChange(false);
    // Either outcome, including a lost response, is settled by the server's list.
    void client.invalidateQueries({ queryKey: getListSessionsQueryKey() });
  }

  function startWrite(): void {
    end.reset();
    endOthers.reset();
  }

  function endSession(session: ListedSession): void {
    if (locked || pending) return;
    const name = browserName(session);
    startWrite();
    setEndedName(name);
    end.mutate({ id: session.id });
  }

  function endOtherSessions(): void {
    setConfirming(false);
    if (locked || pending) return;
    startWrite();
    endOthers.mutate();
  }

  return (
    <SettingsSection title={sessionsTitle} about={sessionsAbout} headingRef={headingRef}>
      {/* Stay mounted so screen readers announce changed text. */}
      <output className={notice ? undefined : "sr-only"}>{notice}</output>
      {end.error && end.error.status !== unauthorized && (
        <p role="alert">{endMessage(end.error, endedName)}</p>
      )}
      {endOthers.error && endOthers.error.status !== unauthorized && (
        <p role="alert">{endMessage(endOthers.error)}</p>
      )}
      {list.isPending && <output>Loading signed-in browsers...</output>}
      {list.error && list.error.status !== unauthorized && (
        <div className="flex flex-col items-start gap-3" role="alert">
          <p>Could not load signed-in browsers. {userMessage(list.error)}</p>
          <Button
            type="button"
            variant="outline"
            className={buttonClass}
            isDisabled={locked || pending || list.isFetching}
            onPress={() => void list.refetch()}
          >
            Try again
          </Button>
        </div>
      )}
      {sessions && (
        <ul className="flex w-full flex-col divide-y" aria-busy={list.isFetching}>
          {sessions.map((session) => (
            <SessionRow
              key={session.id}
              session={session}
              now={list.dataUpdatedAt}
              ending={end.isPending && end.variables?.id === session.id}
              disabled={locked || pending}
              onEnd={() => endSession(session)}
            />
          ))}
        </ul>
      )}
      <p className="text-sm text-muted-foreground">{signOutLimit}</p>
      {others > 0 && (
        <AlertDialogTrigger isOpen={confirming && others > 0} onOpenChange={setConfirming}>
          <Button
            type="button"
            variant="outline"
            className={buttonClass}
            // Pending, not disabled: focus comes back here when the dialog
            // closes, and stays while the write runs.
            isDisabled={locked || (pending && !endOthers.isPending)}
            isPending={endOthers.isPending}
          >
            {endOthers.isPending
              ? "Signing out of other browsers..."
              : "Sign out of all other browsers"}
          </Button>
          <AlertDialog>
            <AlertDialogHeader>
              <AlertDialogTitle>Sign out of all other browsers?</AlertDialogTitle>
              <AlertDialogDescription>
                Every browser except this one will need to sign in again.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction type="button" onPress={endOtherSessions}>
                Sign out of other browsers
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialog>
        </AlertDialogTrigger>
      )}
      {children}
    </SettingsSection>
  );
}

function SessionRow({
  session,
  now,
  ending,
  disabled,
  onEnd,
}: {
  session: ListedSession;
  now: number;
  ending: boolean;
  disabled: boolean;
  onEnd: () => void;
}) {
  const name = browserName(session);
  const detailsId = `session-${session.id}`;

  return (
    // A grid, so the button keeps its side however the name wraps.
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 py-3 first:pt-0">
      <div className="flex min-w-0 flex-col gap-1">
        <p className="flex flex-wrap items-center gap-2 font-medium">
          {name}
          {session.current && (
            <StatusBadge tone="neutral" icon={MonitorIcon}>
              This browser
            </StatusBadge>
          )}
        </p>
        <p id={detailsId} className="text-sm text-muted-foreground">
          Signed in <time dateTime={session.signedInAt}>{formatTime(session.signedInAt)}</time>
          {session.signedInFrom && ` from ${session.signedInFrom}`}
        </p>
        <p className="text-sm text-muted-foreground">{lastActive(session.lastActiveAt, now)}</p>
      </div>
      {!session.current && (
        <Button
          type="button"
          variant="outline"
          className={buttonClass}
          aria-describedby={detailsId}
          isDisabled={disabled && !ending}
          isPending={ending}
          onPress={onEnd}
        >
          {ending ? "Signing out..." : "Sign out"}
          <span className="sr-only"> {name}</span>
        </Button>
      )}
    </li>
  );
}
