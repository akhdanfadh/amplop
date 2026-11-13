import type { ReactElement, ReactNode } from "react";
import { Frame } from "@/components/frame";
import { MailNav } from "./nav";

// MailFrame is the mail frame around children, a place's panes, the
// Outbox, or a new message: the mail sidebar beside them.
export function MailFrame({ children }: { children: ReactNode }): ReactElement {
  return (
    <Frame label="Mail" nav={<MailNav />}>
      {children}
    </Frame>
  );
}
