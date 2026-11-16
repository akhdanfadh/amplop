import type { ReactElement } from "react";
import { CheckIcon, CircleHelpIcon, CircleXIcon, ClockIcon, RotateCwIcon } from "lucide-react";
import type { OutgoingMessageSummary } from "@/api/generated/model";
import { StatusBadge, StatusMark, type StatusIcon, type Tone } from "@/components/status-badge";
import { Spinner } from "@/components/ui/spinner";
import { sentAgainLabel, statusLabel } from "./text";

type Status = OutgoingMessageSummary["status"];

// Each status's tone and icon. Not sent is a fault; Not confirmed asks the
// member to look before sending again (DESIGN.md "Status badges").
const looks: Record<Status, { tone: Tone; icon: StatusIcon }> = {
  waiting: { tone: "neutral", icon: ClockIcon },
  sending: { tone: "info", icon: Spinner },
  sent: { tone: "success", icon: CheckIcon },
  failed: { tone: "destructive", icon: CircleXIcon },
  unknown: { tone: "warning", icon: CircleHelpIcon },
};

// MessageStatusBadge shows status as a badge, e.g. "Not sent".
export function MessageStatusBadge({ status }: { status: Status }): ReactElement {
  const { tone, icon } = looks[status];
  return (
    <StatusBadge tone={tone} icon={icon}>
      {statusLabel(status)}
    </StatusBadge>
  );
}

// SentAgainBadge marks a message whose copy was sent again.
export function SentAgainBadge(): ReactElement {
  return (
    <StatusBadge tone="neutral" icon={RotateCwIcon}>
      {sentAgainLabel}
    </StatusBadge>
  );
}

// MessageStatusMark shows status as a tinted circle with its icon, beside
// words that name it.
export function MessageStatusMark({ status }: { status: Status }): ReactElement {
  const { tone, icon } = looks[status];
  return <StatusMark tone={tone} icon={icon} />;
}
