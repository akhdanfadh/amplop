import { CircleAlertIcon, TriangleAlertIcon } from "lucide-react";
import type { ListedAccount } from "@/api/generated/model";
import type { StatusIcon, Tone } from "@/components/status-badge";
import { Spinner } from "@/components/ui/spinner";
import { syncView } from "@/features/connected-accounts/sync";
import { failureWords, formatMoment } from "@/features/connected-accounts/text";
import {
  needsReconnectDetail,
  reconnectWords,
  syncFailedWords,
  syncingDetail,
  syncingWords,
  syncWaitingDetail,
  tokenUnreadableDetail,
} from "./text";

// AccountStatus is what the sidebar says of an account that needs a look
// or is busy: its words and their tone and icon, and details on hover.
export type AccountStatus = { words: string; tone: Tone; icon: StatusIcon; detail: string };

// accountStatus returns what the sidebar says of account, or undefined
// while it is up to date, which says nothing (DESIGN.md "Status badges").
export function accountStatus(account: ListedAccount): AccountStatus | undefined {
  if (account.status !== "usable") {
    return {
      words: reconnectWords,
      tone: "warning",
      icon: TriangleAlertIcon,
      detail: account.status === "token-unreadable" ? tokenUnreadableDetail : needsReconnectDetail,
    };
  }
  const view = syncView(account.sync);
  if (view.kind === "waiting")
    return { words: syncingWords, tone: "info", icon: Spinner, detail: syncWaitingDetail };
  if (view.kind === "running")
    return {
      words: syncingWords,
      tone: "info",
      icon: Spinner,
      detail: syncingDetail(view.messagesSoFar),
    };
  if (view.kind === "failed") {
    const { before, after } = failureWords(view.failure, view.status);
    return {
      words: syncFailedWords,
      tone: "destructive",
      icon: CircleAlertIcon,
      detail: `${before} ${formatMoment(view.failedAt)}${after}`,
    };
  }
  return undefined;
}

// accountDetail returns what an account's row says on hover or focus: its
// name, when cut is true, then its status's words and details, which the
// row shows as an icon alone; or "" when there is nothing more to say.
export function accountDetail(
  name: string,
  cut: boolean,
  status: AccountStatus | undefined,
): string {
  return [cut && name, status && `${status.words}. ${status.detail}`].filter(Boolean).join(". ");
}
