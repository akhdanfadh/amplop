// An address in a message's header: the display name the sender gave, if
// any, and the email address.
export type Address = { name?: string; email: string };

const noSubject = "(no subject)";
const shownTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const fullTime = new Intl.DateTimeFormat(undefined, { dateStyle: "full", timeStyle: "long" });

// addressName returns the display name to show for address, or undefined
// when it has none or the name only repeats the email.
export function addressName(address: Address): string | undefined {
  const name = address.name?.trim();
  if (!name || name === address.email) return undefined;
  return name;
}

// subjectText shows subject, or says the message has none.
export function subjectText(subject: string): string {
  return subject.trim() || noSubject;
}

// formatReceived shows a timestamp in the reader's locale and time zone,
// short for the header and full for its tooltip. A value that is not a
// time shows as given, rather than failing the whole view.
export function formatReceived(iso: string): { short: string; full: string } {
  const time = new Date(iso);
  if (Number.isNaN(time.getTime())) return { short: iso, full: iso };
  return { short: shownTime.format(time), full: fullTime.format(time) };
}
