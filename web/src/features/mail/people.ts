// Who mail names (DESIGN.md "Message list", "Reading pane"): a list row's
// senders by first name, a header's in full, "me" for the account itself,
// and a sender's initials.

import type { MailAddressOutput as Address } from "@/api/generated/model";
import { me } from "./text";

// senders returns how a row names participants, the senders of its
// messages in date order: one sender by their whole name, several by first
// name; own, the account's address, as "me". An entry with no name goes by
// its address.
export function senders(participants: readonly Address[], own: string | undefined): string[] {
  return participants.map((participant) =>
    isOwn(participant, own)
      ? me
      : participants.length === 1
        ? whole(participant)
        : first(participant),
  );
}

// fullNames returns how a message's header names addresses: each by its
// whole name, or its address without one; own, the account's address, as
// "me".
export function fullNames(addresses: readonly Address[], own: string | undefined): string {
  return addresses.map((entry) => (isOwn(entry, own) ? me : whole(entry))).join(", ");
}

// initials returns up to two letters for sender: the first letters of the
// first and last words of its name that start with a letter, or of its
// address; empty when it has none, e.g. a name of emoji alone.
export function initials(sender: Address | undefined): string {
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
export function repliesElsewhere(replyTo: readonly Address[], from: readonly Address[]): boolean {
  const fromDomains = new Set(from.map(domain));
  return replyTo.some((entry) => entry.address !== "" && !fromDomains.has(domain(entry)));
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
function whole(entry: Address): string {
  return entry.name.trim() || entry.address;
}

// first returns entry's first name, or the part of its address before @.
function first(entry: Address): string {
  const [word] = entry.name.trim().split(/\s+/);
  if (word) return word;
  const at = entry.address.indexOf("@");
  return at > 0 ? entry.address.slice(0, at) : entry.address;
}

// domain returns the part of entry's address after its last @, in lower
// case: the domain, as an address's local part may itself hold an @ in
// quotes (RFC 5321 section 4.1.2).
function domain(entry: Address): string {
  return entry.address.slice(entry.address.lastIndexOf("@") + 1).toLowerCase();
}

// isOwn reports whether entry is the account's own address, own.
function isOwn(entry: Address, own: string | undefined): boolean {
  return own !== undefined && entry.address.toLowerCase() === own.toLowerCase();
}
