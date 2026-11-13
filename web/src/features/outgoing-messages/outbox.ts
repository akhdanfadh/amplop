import { useListOutgoingMessages } from "@/api/generated/inbok";
import { pollWhile } from "./poll";
import { inProgress } from "./text";

// How often the list is read again while a message in it is still being sent.
const pollMs = 5_000;

// useOutbox reads the member's Outbox list, again every 5 s while a message
// in it is still being sent. The sidebar's count and the Outbox page share
// this read, so they show one list.
export function useOutbox() {
  return useListOutgoingMessages({
    query: {
      refetchInterval: pollWhile(pollMs, (data) =>
        data.data.messages.some((message) => inProgress(message.status)),
      ),
    },
  });
}
