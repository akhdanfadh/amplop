import type { ReactElement } from "react";
import { useBlocker } from "@tanstack/react-router";
import { ChoiceDialog, type ChoiceText } from "@/components/choice-dialog";
import { useHeldWork } from "@/features/auth/session-end";

// The words of one leave warning, as a ChoiceDialog asks them.
export type LeaveWarningText = ChoiceText;

// A place in the app as a blocker sees it: its path and its search.
type Address = { pathname: string; search: unknown };

// sameAddress reports whether a and b are the same address.
function sameAddress(a: Address, b: Address): boolean {
  return a.pathname === b.pathname && JSON.stringify(a.search) === JSON.stringify(b.search);
}

// LeaveWarning asks before the member leaves the page while active, e.g.
// while a form holds typed input: a link, Back, or Forward opens a dialog
// with text, and closing or reloading the tab asks with the browser's own
// words; a step to the same address asks nothing. Navigate with
// ignoreBlocker to leave without asking, e.g. after a save. onSessionEnd
// says what a session that ends while it is active does: "stay" keeps the
// page, signed out, so the member can copy what it holds, e.g. a typed
// reply; "leave" opens sign-in, for what would be no use once the session
// is gone, e.g. backup codes that only a signed-in Finish makes work.
export function LeaveWarning({
  active,
  text,
  onSessionEnd,
}: {
  active: boolean;
  text: LeaveWarningText;
  onSessionEnd: "stay" | "leave";
}): ReactElement {
  useHeldWork(active && onSessionEnd === "stay");
  const blocker = useBlocker({
    // Only leaving the page's address leaves it: a step that keeps it, e.g.
    // the history entry the compose window adds on a phone, keeps the page.
    shouldBlockFn: ({ current, next }) => active && !sameAddress(current, next),
    // Browsers show only their own text before unloading a page.
    enableBeforeUnload: active,
    withResolver: true,
  });

  return (
    <ChoiceDialog
      isOpen={blocker.status === "blocked"}
      text={text}
      // Cancel or Escape: stay on the page. An alert dialog does not close
      // on a click outside it.
      onStay={() => blocker.reset?.()}
      onLeave={() => blocker.proceed?.()}
    />
  );
}
