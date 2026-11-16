import { describe, expect, it } from "vite-plus/test";
// The app's words for Zod's own checks, e.g. a length limit, as the form shows them.
import "@/zod-config";
import type { OutgoingMessage } from "@/api/generated/model";
import {
  ComposeSchema,
  composeRequest,
  composeValues,
  editRequest,
  type ComposeValues,
} from "./compose-schema";

const valid: ComposeValues = {
  connectedAccountId: 7,
  to: ["ana@example.com"],
  cc: [],
  bcc: [],
  subject: "Lunch",
  body: "Noon?",
};

// issues returns each refused field's messages.
function issues(values: ComposeValues): Record<string, string[]> {
  const result = ComposeSchema.safeParse(values);
  if (result.success) return {};
  const byField: Record<string, string[]> = {};
  for (const issue of result.error.issues) {
    const field = String(issue.path[0]);
    byField[field] = [...(byField[field] ?? []), issue.message];
  }
  return byField;
}

describe("ComposeSchema", () => {
  it("accepts a message with one recipient in any list", () => {
    expect(issues(valid)).toEqual({});
    expect(issues({ ...valid, to: [], bcc: ["ana@example.com"] })).toEqual({});
  });

  it("asks for a recipient on To when every list is empty", () => {
    expect(issues({ ...valid, to: [] })).toEqual({ to: ["Add at least one recipient."] });
  });

  it("names the first address that isn't one", () => {
    expect(issues({ ...valid, cc: ["bo@example.com", "bo@", "cy"] })).toEqual({
      cc: ["This isn't an email address, so fix it or remove it: bo@"],
    });
  });

  it("refuses an address longer than the contract allows", () => {
    const long = `${"a".repeat(64)}@${"b".repeat(186)}.com`;
    expect(issues({ ...valid, bcc: [long] }).bcc).toHaveLength(1);
  });

  it("refuses more than 100 addresses in one list", () => {
    const many = Array.from({ length: 101 }, (_, i) => `p${i}@example.com`);
    expect(issues({ ...valid, to: many })).toEqual({ to: ["Enter at most 100 addresses."] });
  });

  it("takes exactly 100 addresses, and an address of 254 characters", () => {
    const hundred = Array.from({ length: 100 }, (_, i) => `p${i}@example.com`);
    const longest = `${"a".repeat(64)}@${"b".repeat(185)}.com`;
    expect(longest).toHaveLength(254);
    expect(issues({ ...valid, to: hundred, cc: [longest] })).toEqual({});
  });

  it.each(["a\nb", "a\u007fb", "a\u0085b"])("refuses the control character in %j", (subject) => {
    expect(issues({ ...valid, subject }).subject).toHaveLength(1);
  });

  it("refuses control characters in the subject", () => {
    expect(issues({ ...valid, subject: "a\tb" })).toEqual({
      subject: ["Enter a subject without line breaks or other control characters."],
    });
  });

  it("states the subject and body length limits", () => {
    expect(issues({ ...valid, subject: "s".repeat(501), body: "b".repeat(100_001) })).toEqual({
      subject: ["Must be at most 500 characters."],
      body: ["Must be at most 100000 characters."],
    });
  });

  it("asks for an account", () => {
    expect(issues({ ...valid, connectedAccountId: 0 })).toEqual({
      connectedAccountId: ["Choose the account to send from."],
    });
  });
});

describe("composeRequest", () => {
  it("sends each list as its addresses", () => {
    expect(
      composeRequest({
        ...valid,
        to: ["ana@example.com", "bo@example.com"],
        bcc: ["cy@example.com"],
      }),
    ).toEqual({
      connectedAccountId: 7,
      to: ["ana@example.com", "bo@example.com"],
      cc: [],
      bcc: ["cy@example.com"],
      subject: "Lunch",
      body: "Noon?",
    });
  });
});

describe("composeValues", () => {
  const message: OutgoingMessage = {
    id: "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11",
    connectedAccountId: 9,
    from: "me@example.com",
    to: ["ana@example.com", "bo@example.com"],
    cc: [],
    bcc: ["cy@example.com"],
    subject: "Lunch",
    body: "Noon?",
    status: "failed",
    failure: "refused",
    createdAt: "2026-11-10T10:00:00Z",
    sentAt: null,
    sentAgainAs: null,
    reply: false,
    allowedActions: ["send-again"],
  };

  it("starts empty, with no account chosen", () => {
    expect(composeValues()).toEqual({
      connectedAccountId: 0,
      to: [],
      cc: [],
      bcc: [],
      subject: "",
      body: "",
    });
  });

  it("starts edits with a message's content, from its own account", () => {
    expect(composeValues(message)).toEqual({
      connectedAccountId: 9,
      to: ["ana@example.com", "bo@example.com"],
      cc: [],
      bcc: ["cy@example.com"],
      subject: "Lunch",
      body: "Noon?",
    });
  });
});

describe("editRequest", () => {
  const values = {
    to: ["ana@example.com"],
    cc: [],
    bcc: [],
    subject: "Lunch, fixed",
    body: "Noon?",
  };

  it("sends the edited content", () => {
    expect(editRequest({ reply: false, subject: "Lunch" }, values)).toEqual(values);
  });

  // The API keeps a reply's subject, which threads it.
  it("keeps a reply's own subject", () => {
    expect(editRequest({ reply: true, subject: "Re: Lunch" }, values).subject).toBe("Re: Lunch");
    expect(
      editRequest({ reply: true, subject: "Re: Lunch" }, { ...values, subject: undefined }).subject,
    ).toBe("Re: Lunch");
  });
});
