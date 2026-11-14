// Who a conversation's messages name (DESIGN.md "Reading pane").
//
// DESIGN: initials and names follow branch 3's features/mail/people.ts,
// which is not on dev yet; whichever branch merges later keeps one copy.

import type { MailAddress } from "@/features/outgoing-messages/reply-defaults";
import { me } from "./text";

// MailAddress is one entry of a From, To, Cc, or Reply-To, as the API
// sends it: address is empty for an entry that is none, e.g. a group's
// name ("Undisclosed recipients"), which is then the name.
export type { MailAddress } from "@/features/outgoing-messages/reply-defaults";

// namesOf returns how a header names addresses: each by its whole name, or
// its address without one; own, the account's address, as "me".
export function namesOf(addresses: readonly MailAddress[], own: string): string {
  return addresses.map((entry) => (isOwn(entry, own) ? me : whole(entry))).join(", ");
}

// initials returns up to two letters for sender: the first letters of the
// first and last words of its name that start with a letter, or of its
// address; empty when it has none, e.g. a name of emoji alone.
export function initials(sender: MailAddress | undefined): string {
  if (!sender) return "";
  const words = [sender.name, sender.address]
    .map((text) => text.trim().split(/\s+/).map(firstLetter).filter(Boolean))
    .find((letters) => letters.length > 0);
  if (!words) return "";
  const picked = words.length > 1 ? [words[0], words.at(-1)] : [words[0]];
  return picked.join("").toUpperCase();
}

// repliesElsewhere reports whether replyTo sends replies to a domain none
// of from's addresses is at. A Reply-To at the sender's own domain, e.g.
// news@ answered at help@, is common and harmless, so only another domain
// is worth calling out (inbok's choice).
export function repliesElsewhere(
  replyTo: readonly MailAddress[],
  from: readonly MailAddress[],
): boolean {
  const senders = new Set(from.map(domain));
  return replyTo.some((entry) => entry.address !== "" && !senders.has(domain(entry)));
}

// Splits text into what a reader sees as single characters, so an accent
// or an emoji's parts stay with their letter.
const graphemes = new Intl.Segmenter("en", { granularity: "grapheme" });

// firstLetter returns word's first character if it is a letter, e.g. not
// an emoji or a quote mark, else empty.
function firstLetter(word: string): string {
  const [character] = graphemes.segment(word);
  return character && /^\p{L}/u.test(character.segment) ? character.segment : "";
}

// whole returns the name entry goes by in full.
function whole(entry: MailAddress): string {
  return entry.name.trim() || entry.address;
}

// domain returns the part of entry's address after its last @, in lower
// case: the domain, as an address's local part may itself hold an @ in
// quotes (RFC 5321 section 4.1.2).
function domain(entry: MailAddress): string {
  return entry.address.slice(entry.address.lastIndexOf("@") + 1).toLowerCase();
}

// isOwn reports whether entry is the account's own address, own.
function isOwn(entry: MailAddress, own: string): boolean {
  return entry.address.toLowerCase() === own.toLowerCase();
}
