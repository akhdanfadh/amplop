import type { ViewPlace } from "./places";

// Each view's name, as the sidebar and the list's heading show it.
export const placeName: Record<ViewPlace, string> = {
  inbox: "Inbox",
  starred: "Starred",
  sent: "Sent",
  all: "All mail",
  spam: "Spam",
  trash: "Trash",
};

// The sidebar's other names.
export const composeName = "Compose";
export const outboxName = "Outbox";

// What the Outbox's count counts, read after it.
export const notSentLabel = "not sent";

// Shown in the list pane until conversations are built.
export const listPending = "The conversation list isn't built yet.";

// Shown in the reading pane while no conversation is open.
export const nothingOpen = "No conversation open.";
