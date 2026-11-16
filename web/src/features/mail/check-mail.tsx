import { useState, type ReactElement } from "react";
import { onlineManager, useQueryClient } from "@tanstack/react-query";
import { RefreshCwIcon } from "lucide-react";
import { useListConnectedAccounts, useSyncConnectedAccount } from "@/api/generated/inbok";
import { isSessionEnded } from "@/features/auth/guard";
import { ApiError } from "@/api/transport";
import { accountName } from "@/components/account-mark";
import { Button } from "@/components/ui/button";
import { showError } from "@/components/ui/toast";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { currentMail, mailData } from "./conversations";
import {
  accountsNotRead,
  checkFailedTitle,
  checkFailures,
  checkRefusal,
  checkMailName,
  noAccountToCheck,
  offlineCheck,
} from "./text";

// One turn of the icon, Tailwind's animate-spin: the icon turns whole
// turns, so it never snaps back part way.
const turnMs = 1000;

// CheckMail is Check for new mail (DESIGN.md "Message list"): it asks the
// server to check each account the list shows, accountId's or every one,
// for new mail now instead of at the next minute, skipping an account
// whose allowedActions lack sync, then reads the mail pages often while
// those checks run. A refusal shows in an error toast; the list stays as
// it is. An ended session is the session-end rule's (watchSessionEnd).
export function CheckMail({ accountId }: { accountId: number | undefined }): ReactElement {
  const client = useQueryClient();
  const accounts = useListConnectedAccounts();
  const sync = useSyncConnectedAccount({ mutation: { gcTime: 0 } });
  const [checking, setChecking] = useState(false);

  async function check(): Promise<void> {
    // Offline, every request would fail without saying why; the offline
    // banner shows above the content too.
    if (!onlineManager.isOnline()) {
      showError(checkFailedTitle, offlineCheck);
      return;
    }
    const listed = accounts.data?.data.accounts;
    if (!listed) {
      showError(checkFailedTitle, accountsNotRead);
      return;
    }
    const shown = listed.filter((account) => accountId === undefined || account.id === accountId);
    if (shown.length === 0) {
      showError(checkFailedTitle, noAccountToCheck);
      return;
    }
    // The server says which accounts it would check now.
    const syncable = shown.filter((account) => account.allowedActions.includes("sync"));
    if (syncable.length === 0) {
      const refused = shown.map((account) => ({
        name: accountName(account),
        refusal: {
          kind: account.allowedActions.includes("reconnect")
            ? ("reconnect" as const)
            : ("unavailable" as const),
        },
      }));
      showError(checkFailedTitle, checkFailures(refused));
      return;
    }
    // This member's mail, so an answer that comes after they signed out
    // changes nothing and names none of their accounts.
    const mail = mailData(client);
    const started = Date.now();
    setChecking(true);
    const outcomes = await Promise.allSettled(
      syncable.map((account) => sync.mutateAsync({ id: account.id })),
    );
    // The icon ends its turn rather than snapping back.
    const elapsed = Date.now() - started;
    setTimeout(() => setChecking(false), Math.ceil(elapsed / turnMs) * turnMs - elapsed);
    if (currentMail(client) !== mail) return;
    const errors = outcomes.flatMap((outcome) =>
      outcome.status === "rejected" ? [asApiError(outcome.reason)] : [],
    );
    // The session-end rule answers an ended session.
    if (errors.some(isSessionEnded)) return;
    const refused = outcomes.flatMap((outcome, i) => {
      if (outcome.status !== "rejected") return [];
      const refusal = checkRefusal(asApiError(outcome.reason));
      return refusal ? [{ name: accountName(syncable[i]), refusal }] : [];
    });
    // Every check that started, or was running already, brings its mail
    // within seconds; a refusal still leaves the others' to show.
    if (refused.length < outcomes.length) mail.checking();
    if (refused.length > 0) showError(checkFailedTitle, checkFailures(refused));
  }

  return (
    <TooltipTrigger>
      <Button
        type="button"
        variant="ghost"
        // The menu button's size, so a top bar holding both keeps its height.
        size="icon-sm"
        aria-label={checkMailName}
        isPending={checking}
        onPress={() => void check()}
      >
        {/* Reduced motion dims it, still, instead of turning it: a pulse
            cut at a whole turn would end on a jump. */}
        <RefreshCwIcon
          aria-hidden="true"
          className={cn(checking && "motion-safe:animate-spin motion-reduce:opacity-50")}
        />
      </Button>
      <Tooltip>{checkMailName}</Tooltip>
    </TooltipTrigger>
  );
}

// asApiError returns reason as the API client's error; anything else, e.g.
// a fault in the app, counts as a request whose outcome is unknown.
function asApiError(reason: unknown): ApiError {
  return reason instanceof ApiError ? reason : new ApiError("network", { method: "POST" });
}
