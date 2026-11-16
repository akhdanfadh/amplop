import type { ReactElement } from "react";
import { Outlet, createFileRoute, useMatch } from "@tanstack/react-router";
import { OutboxPage } from "@/features/outgoing-messages/outbox-page";
import { outboxName } from "@/features/outgoing-messages/text";

// The Outbox, /mail/outbox: the member's messages from inbok beside the
// reading pane, which holds the open message, /mail/outbox/<message id>,
// or nothing.
export const Route = createFileRoute("/_app/_mail/mail/outbox")({
  component: Outbox,
  head: () => ({ meta: [{ title: `${outboxName} - inbok` }] }),
});

function Outbox(): ReactElement {
  // The reading pane's index shows when no message is open.
  const nothingOpen = useMatch({ from: "/_app/_mail/mail/outbox/", shouldThrow: false });
  const open = useMatch({ from: "/_app/_mail/mail/outbox/$id", shouldThrow: false });
  return (
    <OutboxPage
      openId={open?.params.id}
      pane={nothingOpen ? "list" : "reader"}
      reader={<Outlet />}
    />
  );
}
