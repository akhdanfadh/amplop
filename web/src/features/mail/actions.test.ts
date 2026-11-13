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
});
