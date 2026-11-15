import {
  ArchiveIcon,
  ArchiveRestoreIcon,
  InboxIcon,
  MailIcon,
  MailOpenIcon,
  OctagonAlertIcon,
  StarIcon,
  Trash2Icon,
  type LucideIcon,
} from "lucide-react";
import type { Action } from "./rules";

// ActionWords is how the app names one action: label, its button's and
// menu item's name; icon; failed, the error toast's title; and, for an
// action that moves a conversation to another place, done, the Undo
// bar's title, and where, the place it went, which screen readers hear
// with it. An action without done gets no Undo: its result stays in view.
export type ActionWords = {
  label: string;
  icon: LucideIcon;
  failed: string;
  done?: string;
  where?: string;
};

// The words of every action, so an action the API adds fails the build
// until it has them (DESIGN.md "Feedback").
export const actionWords: Record<Action, ActionWords> = {
  archive: {
    label: "Archive",
    icon: ArchiveIcon,
    failed: "Couldn't archive the conversation",
    done: "Archived",
    where: "All mail",
  },
  unarchive: {
    label: "Move to Inbox",
    // Not Archive's box, which looks alike at 16 px, nor the tray of a
    // conversation coming out of Spam or Trash.
    icon: ArchiveRestoreIcon,
    failed: "Couldn't move the conversation to the Inbox",
    done: "Moved to Inbox",
    where: "the Inbox",
  },
  trash: {
    label: "Delete",
    icon: Trash2Icon,
    failed: "Couldn't delete the conversation",
    done: "Moved to Trash",
    where: "Trash",
  },
  untrash: {
    label: "Move to Inbox",
    icon: InboxIcon,
    failed: "Couldn't move the conversation to the Inbox",
    done: "Moved to Inbox",
    where: "the Inbox",
  },
  spam: {
    label: "Report spam",
    icon: OctagonAlertIcon,
    failed: "Couldn't report the conversation as spam",
    done: "Reported as spam",
    where: "Spam",
  },
  unspam: {
    // Gmail's own name, which members know, rather than DESIGN.md's verb
    // and object (decided with the user, 2026-11-15).
    label: "Not spam",
    icon: InboxIcon,
    failed: "Couldn't move the conversation out of Spam",
    done: "Moved to Inbox",
    where: "the Inbox",
  },
  read: { label: "Mark read", icon: MailOpenIcon, failed: "Couldn't mark the conversation read" },
  unread: { label: "Mark unread", icon: MailIcon, failed: "Couldn't mark the conversation unread" },
  star: { label: "Star", icon: StarIcon, failed: "Couldn't star the conversation" },
  unstar: { label: "Star", icon: StarIcon, failed: "Couldn't remove the star" },
};
