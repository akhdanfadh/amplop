import { useRef, type ReactElement } from "react";
import { useBlocker } from "@tanstack/react-router";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

// The words of one leave warning: the dialog's title and description, its
// buttons for staying and for leaving, and whether leaving loses something,
// which marks its button destructive.
export type LeaveWarningText = {
  title: string;
  description: string;
  stay: string;
  leave: string;
  leaveVariant: "destructive" | "default";
};

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
  // Set by the leave button, which also closes the dialog: that close must
  // not cancel the navigation it just let through.
  const leaving = useRef(false);

  return (
    <AlertDialog
      isOpen={blocker.status === "blocked"}
      // Cancel or Escape: stay on the page. An alert dialog does not close
      // on a click outside it.
      onOpenChange={(open) => {
        if (open) return;
        if (leaving.current) {
          leaving.current = false;
          return;
        }
        blocker.reset?.();
      }}
    >
      <AlertDialogHeader>
        <AlertDialogTitle>{text.title}</AlertDialogTitle>
        <AlertDialogDescription>{text.description}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>{text.stay}</AlertDialogCancel>
        <AlertDialogAction
          type="button"
          variant={text.leaveVariant}
          onPress={() => {
            leaving.current = true;
            blocker.proceed?.();
          }}
        >
          {text.leave}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialog>
  );
}
