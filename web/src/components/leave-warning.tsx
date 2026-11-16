import type { ReactElement } from "react";
import { useBlocker } from "@tanstack/react-router";
import { ChoiceDialog, type ChoiceText } from "@/components/choice-dialog";

// The words of one leave warning, as a ChoiceDialog asks them.
export type LeaveWarningText = ChoiceText;

// LeaveWarning asks before the member leaves the page while active, e.g.
// while a form holds typed input: a link, Back, or Forward opens a dialog
// with text, and closing or reloading the tab asks with the browser's own
// words. Navigate with ignoreBlocker to leave without asking, e.g. after a
// save.
export function LeaveWarning({
  active,
  text,
}: {
  active: boolean;
  text: LeaveWarningText;
}): ReactElement {
  const blocker = useBlocker({
    shouldBlockFn: () => active,
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
