import { useRef, type ReactElement } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

// The words of one question before something is lost: the dialog's title
// and description, its buttons for staying and for going on, and whether
// going on loses something, which marks its button destructive.
export type ChoiceText = {
  title: string;
  description: string;
  stay: string;
  leave: string;
  leaveVariant: "destructive" | "default";
};

// ChoiceDialog asks text's question while isOpen, before something is
// lost: onStay runs on its stay button or Escape, onLeave on its other
// button, e.g. LeaveWarning's question before the member leaves a page.
export function ChoiceDialog({
  isOpen,
  text,
  onStay,
  onLeave,
}: {
  isOpen: boolean;
  text: ChoiceText;
  onStay: () => void;
  onLeave: () => void;
}): ReactElement {
  // Set by the leave button, which also closes the dialog: that close must
  // not count as staying.
  const leaving = useRef(false);
  return (
    <AlertDialog
      isOpen={isOpen}
      onOpenChange={(open) => {
        if (open) return;
        if (leaving.current) {
          leaving.current = false;
          return;
        }
        onStay();
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
            onLeave();
          }}
        >
          {text.leave}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialog>
  );
}
