import { describe, expect, it } from "vite-plus/test";
import { conversation } from "@/test/mail-api";
import { toConversation } from "./conversations";
import { guess, guessChanges, type Action, type Request } from "./rules";

// act returns action as a request; a guess reads no IDs.
const act = (action: Action): Request => ({ action, ids: [] });

describe("guessChanges", () => {
  it("is false when the conversation already is as the action leaves it", () => {
    const starred = toConversation(conversation(1, { starred: true }));
    expect(guessChanges(starred, act("star"))).toBe(false);
    expect(guessChanges(starred, act("unstar"))).toBe(true);
  });

  it("counts a trashed conversation's labels", () => {
    const labelled = toConversation(
      conversation(1, { trash: true, inbox: false, allMail: false, labelIds: ["work"] }),
    );
    expect(guessChanges(labelled, act("trash"))).toBe(true);
    expect(guessChanges({ ...labelled, labelIds: [] }, act("trash"))).toBe(false);
  });

  it("leaves Sent with Trash, as the server's rows show it", () => {
    const sent = toConversation(conversation(1, { inbox: false, sent: true }));
    expect(guessChanges(sent, act("trash"))).toBe(true);
    expect(guessChanges({ ...sent, sent: false, allMail: false, trash: true }, act("trash"))).toBe(
      false,
    );
  });

  it("returns an untrashed conversation to the inbox, as the server does", () => {
    const trashed = toConversation(conversation(1, { trash: true, inbox: false, allMail: false }));
    expect(guessChanges(trashed, act("untrash"))).toBe(true);
    expect(
      guessChanges({ ...trashed, trash: false, allMail: true, inbox: true }, act("untrash")),
    ).toBe(false);
    expect(guessChanges({ ...trashed, trash: false, allMail: true }, act("untrash"))).toBe(true);
  });

  it("is false for spam only in Spam, true for one in a label", () => {
    const onlySpam = toConversation(conversation(1, { spam: true, inbox: false, allMail: false }));
    expect(guessChanges(onlySpam, act("spam"))).toBe(false);
    expect(guessChanges({ ...onlySpam, labelIds: ["work"] }, act("spam"))).toBe(true);
    expect(guessChanges(onlySpam, act("unspam"))).toBe(true);
  });
});

describe("guess", () => {
  it("returns the conversation with the action's flags, the rest as it was", () => {
    const row = toConversation(conversation(1, { unread: true, labelIds: ["work"] }));
    expect(guess(row, act("archive"))).toEqual({ ...row, inbox: false });
    expect(guess(row, act("spam"))).toEqual({
      ...row,
      spam: true,
      inbox: false,
      allMail: false,
      sent: false,
      labelIds: [],
    });
    expect(row.inbox).toBe(true);
  });
});
