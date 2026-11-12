import type { ReactElement } from "react";
import { Link } from "@tanstack/react-router";
import type { OutgoingMessageSummary } from "@/api/generated/model";
import { Badge } from "@/components/ui/badge";
import { formatReceived, subjectText } from "@/features/messages/text";
import { recipientSummary, statusLabel, statusVariant } from "./text";

const focusClass = "focus-visible:focus-ring";

// OutgoingMessageList lists messages, the member's newest first, each a
// link to its own page with its status.
//
// DESIGN: the list has no aria-busy: the page reads it again every few
// seconds while a message is being sent, and marking each read busy would
// make screen readers hold back or restart the list.
export function OutgoingMessageList({
  messages,
}: {
  messages: OutgoingMessageSummary[];
}): ReactElement {
  if (messages.length === 0) return <p>You haven&apos;t sent any messages from inbok yet.</p>;
  return (
    <ul className="flex flex-col divide-y border-y">
      {messages.map((message) => (
        <MessageRow key={message.id} message={message} />
      ))}
    </ul>
  );
}

function MessageRow({ message }: { message: OutgoingMessageSummary }): ReactElement {
  const saved = formatReceived(message.createdAt);
  return (
    <li className="flex flex-col gap-1 py-4">
      <p className="flex flex-wrap items-center gap-2">
        <Link
          to="/outbox/$id"
          params={{ id: message.id }}
          className={`inline-flex min-h-11 min-w-0 items-center font-medium wrap-break-word underline underline-offset-4 ${focusClass}`}
        >
          {subjectText(message.subject)}
        </Link>
        <Badge variant={statusVariant(message.status)}>{statusLabel(message.status)}</Badge>
        {message.sentAgainAs !== null && <Badge variant="outline">Tried again</Badge>}
      </p>
      <p className="text-sm wrap-break-word">
        Recipients: <span translate="no">{recipientSummary(message)}</span>
      </p>
      <p className="text-sm wrap-break-word text-muted-foreground">
        From <span translate="no">{message.from}</span>,{" "}
        <time dateTime={message.createdAt} title={saved.full}>
          {saved.short}
        </time>
      </p>
    </li>
  );
}
