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
// menu item's name; icon; and failed, the error toast's title.
export type ActionWords = {
  label: string;
  icon: LucideIcon;
  failed: string;
};

// The words of every action, so an action the API adds fails the build
// until it has them (DESIGN.md "Feedback").
export const actionWords: Record<Action, ActionWords> = {
  archive: {
    label: "Archive",
    icon: ArchiveIcon,
    failed: "Couldn't archive the conversation",
  },
  unarchive: {
    label: "Move to Inbox",
    // Not Archive's box, which looks alike at 16 px, nor the tray of a
    // conversation coming out of Spam or Trash.
    icon: ArchiveRestoreIcon,
    failed: "Couldn't move the conversation to the Inbox",
  },
  trash: {
    label: "Delete",
    icon: Trash2Icon,
    failed: "Couldn't delete the conversation",
  },
  untrash: {
    label: "Move to Inbox",
    icon: InboxIcon,
    failed: "Couldn't move the conversation to the Inbox",
  },
  spam: {
    label: "Report spam",
    icon: OctagonAlertIcon,
    failed: "Couldn't report the conversation as spam",
  },
  unspam: {
    // Gmail's own name, which members know, rather than DESIGN.md's verb
    // and object (decided with the user, 2026-11-15).
    label: "Not spam",
    icon: InboxIcon,
    failed: "Couldn't move the conversation out of Spam",
  },
  read: { label: "Mark read", icon: MailOpenIcon, failed: "Couldn't mark the conversation read" },
  unread: { label: "Mark unread", icon: MailIcon, failed: "Couldn't mark the conversation unread" },
  star: { label: "Star", icon: StarIcon, failed: "Couldn't star the conversation" },
  unstar: { label: "Star", icon: StarIcon, failed: "Couldn't remove the star" },
};
