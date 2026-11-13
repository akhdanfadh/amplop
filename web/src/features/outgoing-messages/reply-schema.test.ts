import { describe, expect, it } from "vite-plus/test";
// The app's words for Zod's own checks, e.g. a length limit, as the form shows them.
import "@/zod-config";
import { newReplyBodyMax } from "@/api/generated/model";
import type { ReplyValues } from "./reply-defaults";
import { ReplySchema, replyRequest } from "./reply-schema";

const valid: ReplyValues = {
  connectedAccountId: 7,
  to: ["ana@example.com"],
  cc: [],
  bcc: [],
  body: "Noon works.",
};

// issues returns each refused field's messages.
function issues(values: ReplyValues): Record<string, string[]> {
  const result = ReplySchema.safeParse(values);
  if (result.success) return {};
  const byField: Record<string, string[]> = {};
  for (const issue of result.error.issues) {
    const field = String(issue.path[0]);
    byField[field] = [...(byField[field] ?? []), issue.message];
  }
  return byField;
}

describe("ReplySchema", () => {
  it("accepts a reply with one recipient and an empty body", () => {
    expect(issues({ ...valid, body: "" })).toEqual({});
  });

  it("asks for an account and a recipient", () => {
    expect(issues({ ...valid, connectedAccountId: 0, to: [] })).toEqual({
      connectedAccountId: ["Choose the account to send from."],
      to: ["Add at least one recipient."],
    });
  });

  it("checks addresses and the body's limit as Send Message does", () => {
    expect(issues({ ...valid, cc: ["bo@"], body: "x".repeat(newReplyBodyMax + 1) })).toEqual({
      cc: ['"bo@" isn\'t an email address. Fix it or remove it.'],
      body: [`Must be at most ${newReplyBodyMax} characters.`],
    });
  });
});

describe("replyRequest", () => {
  it("sends each list as its addresses, with no subject", () => {
    expect(
      replyRequest({
        ...valid,
        to: ["ana@example.com", "bo@example.com"],
        bcc: ["cy@example.com"],
      }),
    ).toEqual({
      connectedAccountId: 7,
      to: ["ana@example.com", "bo@example.com"],
      cc: [],
      bcc: ["cy@example.com"],
      body: "Noon works.",
    });
  });
});
