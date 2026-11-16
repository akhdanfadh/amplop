import {
  ArchiveIcon,
  ArchiveRestoreIcon,
  FolderInputIcon,
  InboxIcon,
  MailIcon,
  MailOpenIcon,
  OctagonAlertIcon,
  StarIcon,
  StarOffIcon,
  TagIcon,
  Trash2Icon,
  type LucideIcon,
} from "lucide-react";
import type { Action } from "./rules";
import { conversationCount } from "./text";

// ActionWords is how the app names one action: label, its button's and
// menu item's name; icon; failed, the error toast's title, for the one
// conversation acted on or for how many of several it failed on (count);
// done, the Undo bar's title; and, for an
// action that moves a conversation to another place, where, the place it
// went, which screen readers hear with it. One conversation's action
// without where gets no Undo: its result stays in view; several
// conversations' always do, as which of them it changed does not. One
// whose result can be out of view without moving the conversation,
// e.g. a label added, which a row may not show (it shows at most two),
// has an Undo always (alwaysUndo).
export type ActionWords = {
  label: string;
  icon: LucideIcon;
  failed: (count?: number) => string;
  done: string;
  where?: string;
  alwaysUndo?: true;
};

// them names the conversations an action's words take: "the
// conversation" when it acted on one, else count of them, e.g. "3
// conversations".
function them(count: number | undefined): string {
  return count === undefined ? "the conversation" : conversationCount(count);
}

// The words of every action, so an action the API adds fails the build
// until it has them (DESIGN.md "Feedback").
export const actionWords: Record<Action, ActionWords> = {
  archive: {
    label: "Archive",
    icon: ArchiveIcon,
    failed: (count) => `Couldn't archive ${them(count)}`,
    done: "Archived",
    where: "All mail",
  },
  unarchive: {
    label: "Move to Inbox",
    // Not Archive's box, which looks alike at 16 px, nor the tray of a
    // conversation coming out of Spam or Trash.
    icon: ArchiveRestoreIcon,
    failed: (count) => `Couldn't move ${them(count)} to the Inbox`,
    done: "Moved to Inbox",
    where: "the Inbox",
  },
  trash: {
    label: "Delete",
    icon: Trash2Icon,
    failed: (count) => `Couldn't delete ${them(count)}`,
    done: "Moved to Trash",
    where: "Trash",
  },
  untrash: {
    label: "Move to Inbox",
    icon: InboxIcon,
    failed: (count) => `Couldn't move ${them(count)} to the Inbox`,
    done: "Moved to Inbox",
    where: "the Inbox",
  },
  spam: {
    label: "Report spam",
    icon: OctagonAlertIcon,
    failed: (count) => `Couldn't report ${them(count)} as spam`,
    done: "Reported as spam",
    where: "Spam",
  },
  unspam: {
    // Gmail's own name, which members know, rather than DESIGN.md's verb
    // and object (decided with the user, 2026-11-15).
    label: "Not spam",
    icon: InboxIcon,
    failed: (count) => `Couldn't move ${them(count)} out of Spam`,
    done: "Moved to Inbox",
    where: "the Inbox",
  },
  read: {
    label: "Mark read",
    icon: MailOpenIcon,
    failed: (count) => `Couldn't mark ${them(count)} read`,
    done: "Marked read",
  },
  unread: {
    label: "Mark unread",
    icon: MailIcon,
    failed: (count) => `Couldn't mark ${them(count)} unread`,
    done: "Marked unread",
  },
  // A toolbar's star is one toggle, named Star whether pressed or not; the
  // picking bar and menus name each action.
  star: {
    label: "Star",
    icon: StarIcon,
    failed: (count) => `Couldn't star ${them(count)}`,
    done: "Starred",
  },
  unstar: {
    label: "Remove star",
    icon: StarOffIcon,
    failed: (count) =>
      count === undefined
        ? "Couldn't remove the star"
        : `Couldn't remove the ${count === 1 ? "star" : "stars"} from ${them(count)}`,
    done: "Star removed",
  },
};

// moveTo names Move to, which opens the labels to move to.
export const moveTo = { label: "Move to", icon: FolderInputIcon };

// labelsName names Labels, which opens the labels to tick.
export const labelsName = { label: "Labels", icon: TagIcon };

// moveWords returns the words of moving conversations to the label of
// path, its name with its parents'.
export function moveWords(path: string): ActionWords {
  return {
    ...moveTo,
    failed: (count) => `Couldn't move ${them(count)} to ${path}`,
    done: `Moved to ${path}`,
    where: path,
  };
}

// labelWords returns the words of adding conversations to the labels of
// added and taking them out of removed's, each named by its path: one
// label's change by its name, any other as labels changed. Always with an
// Undo, as a row shows at most two labels.
export function labelWords(added: readonly string[], removed: readonly string[]): ActionWords {
  const done =
    added.length === 1 && removed.length === 0
      ? `Added to ${added[0]}`
      : added.length === 0 && removed.length === 1
        ? `Removed from ${removed[0]}`
        : "Labels changed";
  return {
    ...labelsName,
    failed: (count) => `Couldn't change labels on ${them(count)}`,
    done,
    alwaysUndo: true,
  };
}
