import type { ReactElement } from "react";
import { CheckIcon, CircleHelpIcon, CircleXIcon, ClockIcon, RotateCwIcon } from "lucide-react";
import type { OutgoingMessageSummary } from "@/api/generated/model";
import {
  StatusBadge,
  StatusMark,
  StatusWord,
  type StatusIcon,
  type Tone,
} from "@/components/status-badge";
import { Spinner } from "@/components/ui/spinner";
import { sentAgainLabel, statusLabel } from "./text";

type Status = OutgoingMessageSummary["status"];

// MessageLook is how a message's status shows: its tone, its icon, and
// its word.
type MessageLook = { tone: Tone; icon: StatusIcon; label: string };

// Each status's tone and icon. Not sent is a fault; Not confirmed asks the
// member to look before sending again (DESIGN.md "Status badges").
const looks: Record<Status, { tone: Tone; icon: StatusIcon }> = {
  waiting: { tone: "neutral", icon: ClockIcon },
  sending: { tone: "info", icon: Spinner },
  sent: { tone: "success", icon: CheckIcon },
  failed: { tone: "destructive", icon: CircleXIcon },
  unknown: { tone: "warning", icon: CircleHelpIcon },
};

// messageLook returns how message's status shows. A message sent again
// is Tried again, neutral: it asks nothing more, and its copy carries the
// status that matters.
export function messageLook(
  message: Pick<OutgoingMessageSummary, "status" | "sentAgainAs">,
): MessageLook {
  if (message.sentAgainAs !== null)
    return { tone: "neutral", icon: RotateCwIcon, label: sentAgainLabel };
  return { ...looks[message.status], label: statusLabel(message.status) };
}

// MessageStatusBadge shows status as a badge, e.g. "Not sent".
export function MessageStatusBadge({ status }: { status: Status }): ReactElement {
  const { tone, icon } = looks[status];
  return (
    <StatusBadge tone={tone} icon={icon}>
      {statusLabel(status)}
    </StatusBadge>
  );
}

// MessageStatusWord shows message's status inline, as its icon and word,
// e.g. on an Outbox row.
export function MessageStatusWord({
  message,
}: {
  message: Pick<OutgoingMessageSummary, "status" | "sentAgainAs">;
}): ReactElement {
  const { tone, icon, label } = messageLook(message);
  return (
    <StatusWord tone={tone} icon={icon}>
      {label}
    </StatusWord>
  );
}

// MessageStatusMark shows message's status as a tinted circle with its
// icon, beside words that name it.
export function MessageStatusMark({
  message,
}: {
  message: Pick<OutgoingMessageSummary, "status" | "sentAgainAs">;
}): ReactElement {
  const { tone, icon } = messageLook(message);
  return <StatusMark tone={tone} icon={icon} />;
}
