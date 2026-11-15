import { describe, expect, it } from "vite-plus/test";
import { conversation } from "@/test/mail-api";
import { toConversation } from "./conversations";
import { countChanges, guess, guessChanges, placesOf, type Action, type Request } from "./rules";

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

describe("placesOf", () => {
  it.each([
    ["an Inbox conversation", {}, ["inbox", "all"]],
    ["a starred one", { starred: true }, ["inbox", "starred", "all"]],
    [
      "one starred only in Trash, out of Starred as All mail",
      { starred: true, inbox: false, allMail: false, trash: true },
      ["trash"],
    ],
    ["an archived one in Sent", { inbox: false, sent: true }, ["sent", "all"]],
    [
      "one in Spam with a label",
      { inbox: false, allMail: false, spam: true, labelIds: ["work"] },
      ["spam", "work"],
    ],
  ])("lists the places of %s", (_, changes, places) => {
    expect(placesOf(toConversation(conversation(1, changes)))).toEqual(places);
  });
});

describe("countChanges", () => {
  const unread = toConversation(conversation(1, { unread: true, labelIds: ["work"] }));

  it("takes one from each place a read conversation is in", () => {
    expect(countChanges(unread, guess(unread, act("read")))).toEqual([
      { accountId: 1, place: "inbox", by: -1 },
      { accountId: 1, place: "all", by: -1 },
      { accountId: 1, place: "work", by: -1 },
    ]);
  });

  it("moves an unread conversation's count from the places it leaves to those it joins", () => {
    expect(countChanges(unread, guess(unread, act("trash")))).toEqual([
      { accountId: 1, place: "trash", by: 1 },
      { accountId: 1, place: "inbox", by: -1 },
      { accountId: 1, place: "all", by: -1 },
      { accountId: 1, place: "work", by: -1 },
    ]);
    expect(countChanges(unread, guess(unread, act("star")))).toEqual([
      { accountId: 1, place: "starred", by: 1 },
    ]);
  });

  it("changes nothing for a read conversation", () => {
    const read = { ...unread, unread: false };
    expect(countChanges(read, guess(read, act("trash")))).toEqual([]);
  });
});
