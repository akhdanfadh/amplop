import { useCallback, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetOutgoingMessageQueryKey,
  useGetOutgoingMessage,
  useListOutgoingMessages,
} from "@/api/generated/inbok";
import type {
  ResponseOutgoingMessages,
  OutgoingMessage,
  OutgoingMessageSummary,
} from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { pollWhile, sendPollMs } from "./poll";
import { inProgress } from "./text";

// useOutbox reads the member's Outbox list, again every 2 s while a message
// in it is waiting or sending. It is the one source of every message's
// status: the sidebar's count, the Outbox, and each status a page shows
// read it, so they never disagree. select, when given, picks what the
// caller reads of it, so it renders again only when that changes; enabled
// false leaves the caller's read off.
export function useOutbox<T = ResponseOutgoingMessages>({
  select,
  enabled = true,
}: { select?: (data: ResponseOutgoingMessages) => T; enabled?: boolean } = {}) {
  return useListOutgoingMessages({
    query: {
      select,
      enabled,
      refetchInterval: pollWhile(sendPollMs, (data) =>
        data.data.messages.some((message) => inProgress(message.status)),
      ),
    },
  });
}

// OutgoingMessageRead is one message as a page shows it. message is
// undefined until its content loads. error is the failure of its read, or
// else of the list that gives its status. retry reads both
// again where they failed, and retrying is true while either reads.
export type OutgoingMessageRead = {
  message: OutgoingMessage | undefined;
  error: ApiError | null;
  retrying: boolean;
  retry: () => void;
};

// useOutgoingMessage reads message id: its content once, from Get Outgoing
// Message, and its status from the Outbox list (useOutbox). enabled false
// reads nothing, e.g. for an ID never put in the API's path.
//
// DESIGN: a message outside the newest 50 isn't listed, so its own read
// gives its status and polls while it is in progress; so does one saved
// after the list was read, until the list is read again. A listed message
// takes its status from the list alone, even when its own read answered
// later: the two may cross a change, and only the list polls, so the read's
// older status could stay. Each write invalidates the list, so a status the
// write changed shows once the list is read again. A message that leaves
// the list, e.g. past 50 newer ones, is read again then, as its own read
// may be older than the list's last status and, ended, wouldn't poll.
export function useOutgoingMessage(
  id: string,
  { enabled = true }: { enabled?: boolean } = {},
): OutgoingMessageRead {
  // null: the list doesn't hold it; undefined: the list hasn't loaded.
  const select = useCallback(
    (data: ResponseOutgoingMessages): OutgoingMessageSummary | null =>
      data.data.messages.find((message) => message.id === id) ?? null,
    [id],
  );
  const listed = useOutbox({ select, enabled });
  const fromList = listed.data ?? undefined;
  // A failed list read stops its polling, so this one waits for it too.
  const unlisted = listed.status === "success" && listed.data === null;
  const client = useQueryClient();
  const read = useGetOutgoingMessage(id, {
    query: {
      enabled,
      // The content never changes, and the list keeps a listed message's
      // status current.
      staleTime: fromList ? Infinity : 0,
      refetchInterval: pollWhile(sendPollMs, (data) => unlisted && inProgress(data.data.status)),
    },
  });
  const { refetch } = read;
  // The ID the list last held, so leaving it rereads that message only.
  const listedId = useRef<string | null>(null);
  useEffect(() => {
    if (fromList) listedId.current = id;
    else if (unlisted && listedId.current === id) {
      listedId.current = null;
      // A read still running may be older than the list's last status,
      // and refetch would join it rather than start another.
      const queryKey = getGetOutgoingMessageQueryKey(id);
      void client.cancelQueries({ queryKey, exact: true }).then(() => refetch());
    }
  }, [id, fromList, unlisted, client, refetch]);
  const content = read.data?.data;
  const message = content && fromList ? { ...content, ...fromList } : content;
  const error = read.error ?? listed.error;
  return {
    message,
    error,
    // Read only with an error to retry: reading isFetching would render
    // every status reader again at each poll of the list.
    retrying: error !== null && (read.isFetching || listed.isFetching),
    retry: () => {
      if (read.error) void read.refetch();
      if (listed.error) void listed.refetch();
    },
  };
}
