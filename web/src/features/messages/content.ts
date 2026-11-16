import { useGetMessageContent } from "@/api/generated/inbok";
import type { MessageContentOutput } from "@/api/generated/model/messageContent.zod";
import type { Attachment } from "./attachments";
import type { MessageBody, MessageContent } from "./view";

// The API's own addresses, the only ones the message view links or frames.
const apiPrefix = "/api/";

// useMessageContent loads the content of the message with id, the API's
// message ID, and returns it as the message view shows it. Try again
// reads it once more.
export function useMessageContent(id: string): MessageContent {
  // A message's content never changes, so it is never read again in the
  // background, e.g. when the window regains focus: each read would cost a
  // request, and for content not stored yet, a call to the provider. A
  // deleted message leaves through the conversation's own data.
  const read = useGetMessageContent(id, { query: { staleTime: Infinity } });
  if (read.data) return toContent(read.data.data);
  if (read.error)
    return {
      status: "error",
      error: read.error,
      retrying: read.isFetching,
      onRetry: () => void read.refetch(),
    };
  return { status: "pending" };
}

// toContent returns content as the message view shows it. A body or file
// whose address is not the API's is left out: the server names only its
// own, so another would be a server bug, which no frame or link follows.
export function toContent(content: MessageContentOutput): MessageContent {
  return {
    status: "success",
    remote: content.remote,
    bodies: content.bodies.flatMap((body): MessageBody[] => {
      if (!isApiPath(body.url)) return [];
      if (body.type === "text")
        return [
          {
            partId: body.partId,
            type: "text",
            text: body.text ?? "",
            wholeUrl: body.clipped ? body.url : undefined,
          },
        ];
      const remoteUrl = body.remoteUrl ?? body.url;
      return isApiPath(remoteUrl)
        ? [{ partId: body.partId, type: "html", url: body.url, remoteUrl }]
        : [];
    }),
    attachments: content.attachments.flatMap((attachment): Attachment[] =>
      isApiPath(attachment.url) ? [{ ...attachment, url: attachment.url }] : [],
    ),
  };
}

function isApiPath(url: string): url is `/api/${string}` {
  return url.startsWith(apiPrefix);
}
