import type { ReactElement } from "react";
import {
  BellOffIcon,
  CheckIcon,
  CircleHelpIcon,
  CircleXIcon,
  ClockIcon,
  RotateCwIcon,
} from "lucide-react";
import type { OutgoingMessageSummary } from "@/api/generated/model";
import { StatusMark, StatusWord, type StatusIcon, type Tone } from "@/components/status-badge";
import { Spinner } from "@/components/ui/spinner";
import { dismissedLabel, sentAgainLabel, statusLabel } from "./text";

type Status = OutgoingMessageSummary["status"];

// MessageLook is how a message's status shows: its tone, its icon, and
// its word.
type MessageLook = { tone: Tone; icon: StatusIcon; label: string };

// LookedMessage is what messageLook needs of a message.
type LookedMessage = Pick<OutgoingMessageSummary, "status" | "sentAgainAs" | "dismissedAt">;

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
// status that matters. A dismissed one is Dismissed, neutral too, its bell
// off: the member chose to leave it; once sent again as well, its copy
// matters more.
export function messageLook(message: LookedMessage): MessageLook {
  if (message.sentAgainAs !== null)
    return { tone: "neutral", icon: RotateCwIcon, label: sentAgainLabel };
  if (message.dismissedAt !== null)
    return { tone: "neutral", icon: BellOffIcon, label: dismissedLabel };
  return { ...looks[message.status], label: statusLabel(message.status) };
}

// MessageStatusWord shows message's status inline, as its icon and word,
// e.g. on an Outbox row.
export function MessageStatusWord({ message }: { message: LookedMessage }): ReactElement {
  const { tone, icon, label } = messageLook(message);
  return (
    <StatusWord tone={tone} icon={icon}>
      {label}
    </StatusWord>
  );
}

// MessageStatusMark shows message's status as a tinted circle with its
// icon, beside words that name it.
export function MessageStatusMark({ message }: { message: LookedMessage }): ReactElement {
  const { tone, icon } = messageLook(message);
  return <StatusMark tone={tone} icon={icon} />;
}
