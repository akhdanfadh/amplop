import { describe, expect, it } from "vite-plus/test";
import { conversation } from "@/test/mail-api";
import { guessChanges } from "./actions";
import { toConversation } from "./conversations";

describe("guessChanges", () => {
  it("is false when the conversation already is as the action leaves it", () => {
    const starred = toConversation(conversation(1, { starred: true }));
    expect(guessChanges(starred, "star")).toBe(false);
    expect(guessChanges(starred, "unstar")).toBe(true);
  });

  it("counts a trashed conversation's labels", () => {
    const labelled = toConversation(
      conversation(1, { trash: true, inbox: false, allMail: false, labelIds: ["work"] }),
    );
    expect(guessChanges(labelled, "trash")).toBe(true);
    expect(guessChanges({ ...labelled, labelIds: [] }, "trash")).toBe(false);
  });

  it("leaves Sent with Trash, as the server's rows show it", () => {
    const sent = toConversation(conversation(1, { inbox: false, sent: true }));
    expect(guessChanges(sent, "trash")).toBe(true);
    expect(guessChanges({ ...sent, sent: false, allMail: false, trash: true }, "trash")).toBe(
      false,
    );
  });

  it("returns an untrashed conversation to the inbox, as the server does", () => {
    const trashed = toConversation(conversation(1, { trash: true, inbox: false, allMail: false }));
    expect(guessChanges(trashed, "untrash")).toBe(true);
    expect(guessChanges({ ...trashed, trash: false, allMail: true, inbox: true }, "untrash")).toBe(
      false,
    );
    expect(guessChanges({ ...trashed, trash: false, allMail: true }, "untrash")).toBe(true);
  });

  it("is false for spam only in Spam, true for one in a label", () => {
    const onlySpam = toConversation(conversation(1, { spam: true, inbox: false, allMail: false }));
    expect(guessChanges(onlySpam, "spam")).toBe(false);
    expect(guessChanges({ ...onlySpam, labelIds: ["work"] }, "spam")).toBe(true);
    expect(guessChanges(onlySpam, "unspam")).toBe(true);
  });
});
