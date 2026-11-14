// An address in a message's header: the display name the sender gave, if
// any, and the email address.
export type Address = { name?: string; email: string };

const shownTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const fullTime = new Intl.DateTimeFormat(undefined, { dateStyle: "full", timeStyle: "long" });

// addressName returns the display name to show for address, or undefined
// when it has none or the name only repeats the email.
export function addressName(address: Address): string | undefined {
  const name = address.name?.trim();
  if (!name || name === address.email) return undefined;
  return name;
}

// formatReceived shows a timestamp in the reader's locale and time zone,
// short for the header and full for its tooltip. A value that is not a
// time shows as given, rather than failing the whole view.
export function formatReceived(iso: string): { short: string; full: string } {
  const time = new Date(iso);
  if (Number.isNaN(time.getTime())) return { short: iso, full: iso };
  return { short: shownTime.format(time), full: fullTime.format(time) };
}

export const loadingMessage = "Loading message...";
export const contentFailed = "Could not load this message.";
export const bodyFailed = "Could not show this part of the message.";

// The images notice, before and after the member shows the images.
export const imagesHidden =
  "inbok hides images from the web. Showing them tells the sender you opened this message.";
export const showImages = "Show images";
export const imagesShown = "Showing images from the web for this message.";

// A plain-text body the server sent only the start of.
export const textClipped = "This message is long, so inbok shows only its start.";
export const downloadText = "Download the whole text";
// The name a downloaded text body takes.
export const textFilename = "message.txt";

// A message with no text body, or only blank ones.
export const noText = "This message has no text.";

// A conversation's words (DESIGN.md "Reading pane").
// How a header's "to" line names a message with no recipients.
export const noRecipients = "no one";
export const conversationActions = "Conversation actions";
export const detailsLabel = "Details";
export const receivedBy = "Received by";
export const notSendersDomain = "Not the sender's domain";
// Where a message being answered was, after sync removed it.
export const answeredDeleted = "The message you're answering was deleted.";
// A folded message known to have attachments.
export const hasAttachments = "Has attachments";
// The line under an open message's sender whose Reply-To is at another
// domain, around the Reply-To addresses.
export const repliesGoTo = "Replies go to";
export const notTheSender = ", not the sender.";
// The labels of an open message's details.
export const fieldLabels = {
  from: "From",
  replyTo: "Reply-To",
  to: "To",
  cc: "Cc",
  date: "Date",
} as const;

// foldLabel names the button that folds sender's open message.
export function foldLabel(sender: string): string {
  return `Fold the message from ${sender}`;
}

// sentTo is an open message's "to" line, given who received it.
export function sentTo(recipients: string): string {
  return `to ${recipients}`;
}

// messageTotal says how many messages a conversation has, e.g. "4
// messages", where the list's messageCount shows the bare number.
export function messageTotal(count: number): string {
  return count === 1 ? "1 message" : `${count.toLocaleString("en")} messages`;
}

// moreMessages names the row that stands for count folded messages.
export function moreMessages(count: number): string {
  return `${count.toLocaleString("en")} more messages`;
}

// showAllAddresses names the link that lists all count addresses of a
// field.
export function showAllAddresses(count: number): string {
  return `Show all ${count.toLocaleString("en")}`;
}

export const attachmentsLabel = "Attachments";
// An attachment the sender gave no name.
export const unnamedFile = "Unnamed file";

const sizeUnits = ["KB", "MB", "GB"];
// Bytes in a KB, and a KB's in an MB, as file managers count them.
const unitBase = 1024;
// Below this many of a unit, a size keeps one decimal.
const fractionBelow = 10;
const sizeNumber = new Intl.NumberFormat("en");
const sizeFraction = new Intl.NumberFormat("en", { maximumFractionDigits: 1 });

// formatSize shows a file's size in bytes, e.g. "640 bytes", "2.4 MB",
// "36 KB": one decimal below 10 of a unit, as file managers do.
export function formatSize(bytes: number): string {
  if (bytes < unitBase) return bytes === 1 ? "1 byte" : `${sizeNumber.format(bytes)} bytes`;
  let value = bytes / unitBase;
  let unit = 0;
  // Rounded before the unit is chosen, so 1,048,575 bytes reads "1 MB",
  // not "1,024 KB".
  while (shown(value) >= unitBase && unit < sizeUnits.length - 1) {
    value /= unitBase;
    unit++;
  }
  const number =
    value < fractionBelow ? sizeFraction.format(value) : sizeNumber.format(Math.round(value));
  return `${number} ${sizeUnits[unit]}`;
}

// shown returns value as formatSize rounds it.
function shown(value: number): number {
  return value < fractionBelow ? Math.round(value * 10) / 10 : Math.round(value);
}

// How many characters before a name's extension stay with it when the
// name is cut, inbok's choice: enough for a version or a date with its
// separator, e.g. "-final-v12" in "Q3-report-final-v12.xlsx", which tells
// similar files apart.
const keptBeforeExtension = 10;
// How much of a name without an extension stays when it is cut.
const keptWithoutExtension = 10;
// The longest extension, its dot included, a chip keeps whole, inbok's
// choice: common ones are 3 to 5 characters, ".numbers" 8.
const maxExtension = 10;
const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

// A name split for a chip: the parts it cuts first and second, and the
// extension, which it never cuts.
export type NameParts = { start: string; beforeExtension: string; extension: string };

// splitName returns name's parts: an extension of at most maxExtension
// characters, its dot included, the up to keptBeforeExtension characters
// before it, and the start; a name without such an extension has its last
// keptWithoutExtension characters in place of the two. It splits between
// characters as people see them, never inside an emoji.
export function splitName(name: string): NameParts {
  const characters = Array.from(graphemes.segment(name), (part) => part.segment);
  const join = (from: number, to?: number): string => characters.slice(from, to).join("");
  const dot = characters.lastIndexOf(".");
  // A leading dot, as in ".env", starts a name; a long "extension" is a
  // sentence after a dot, which would leave no room for the start.
  if (dot > 0 && characters.length - dot <= maxExtension) {
    const before = Math.max(0, dot - keptBeforeExtension);
    return { start: join(0, before), beforeExtension: join(before, dot), extension: join(dot) };
  }
  const end = Math.max(0, characters.length - keptWithoutExtension);
  return { start: join(0, end), beforeExtension: join(end), extension: "" };
}
