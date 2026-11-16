// One entry of a list: entries are separated by commas, semicolons, or
// line breaks, none of which a bare address holds, outside quotes, since a
// quoted display name may hold them (RFC 5322 section 3.4). An unclosed
// quote runs to the end, so its text is kept, not lost.
const entry = /(?:"(?:[^"\\]|\\.)*"?|[^,;\n"])+/g;
// One word of an entry with no <address>: spaces separate addresses there
// too, outside quotes.
const word = /(?:"(?:[^"\\]|\\.)*"?|[^\s"])+/g;
// A display name in quotes, which may hold a < or >.
const quotedName = /"(?:[^"\\]|\\.)*"/g;
// The address in "Name <address>".
const angleAddress = /<([^<>]*)>/;

// parseRecipients returns the addresses in text typed or pasted into a
// recipient list, in order: bare, or the address of "Name <address>",
// as mail clients copy them. Any other text is kept as typed, so the
// form shows it as not an address instead of dropping it.
export function parseRecipients(text: string): string[] {
  return (text.match(entry) ?? []).flatMap((part) => {
    const named = angleAddress.exec(part.replace(quotedName, ""))?.[1].trim();
    if (named) return [named];
    return part.match(word) ?? [];
  });
}
