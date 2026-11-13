import type { ReactElement, ReactNode } from "react";
import { Frame } from "@/components/frame";
import { MailNav } from "./nav";
import { useRememberMailPage } from "./places";

// MailFrame is the mail frame around children, a place's panes, the
// Outbox, or a new message: the mail sidebar, and the page it shows kept
// for settings' Back to mail.
export function MailFrame({ children }: { children: ReactNode }): ReactElement {
  useRememberMailPage();

  return (
    <Frame label="Mail" nav={<MailNav />}>
      {children}
    </Frame>
  );
}
