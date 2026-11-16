import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import * as z from "zod";
import { OutgoingReader } from "@/features/outgoing-messages/reader";

// A message's ID, which the client puts in the API's path as is.
const messageId = z.uuid();

// A message open in the Outbox's reading pane, /mail/outbox/<message id>.
// The reader names the tab after its subject.
export const Route = createFileRoute("/_app/_mail/mail/outbox/$id")({
  component: OutgoingMessage,
});

function OutgoingMessage(): ReactElement {
  const { id } = Route.useParams();
  // Anything else in the address, e.g. ../ segments, would reach another
  // path of the API, so it is never read.
  const valid = messageId.safeParse(id).success;
  return <OutgoingReader id={id} valid={valid} />;
}
