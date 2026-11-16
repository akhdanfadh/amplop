import { describe, expect, it } from "vite-plus/test";
import type { ListedAccount } from "@/api/generated/model";
import { listedAccount } from "@/test/mail-api";
import {
  othersReceived,
  replyDefaults,
  type AnsweredMessage,
  type ReplyMode,
} from "./reply-defaults";

// The member's accounts: Studio received the answered messages.
const studio: ListedAccount = listedAccount(7, "studio@example.com", 1);
const personal: ListedAccount = { ...studio, id: 8, email: "me@example.org" };
const accounts = [studio, personal];

// address returns a mail address with a display name, as sync stores it.
function address(addr: string, name = "") {
  return { name, address: addr };
}

// A message Ana sent to Studio and Ben, copying Cy.
const received: AnsweredMessage = {
  id: "019a0000-0000-7000-8000-000000000001",
  accountId: studio.id,
  from: [address("ana@example.com", "Ana")],
  to: [address("studio@example.com", "Studio"), address("ben@example.com")],
  cc: [address("cy@example.com")],
  replyTo: [],
  sent: false,
};

describe("replyDefaults", () => {
  const cases: {
    name: string;
    message: AnsweredMessage;
    mode: ReplyMode;
    to: string[];
    cc: string[];
  }[] = [
    {
      name: "reply goes to the sender",
      message: received,
      mode: "reply",
      to: ["ana@example.com"],
      cc: [],
    },
    {
      name: "reply goes to Reply-To instead",
      message: { ...received, replyTo: [address("list@example.com"), address("ana@work.example")] },
      mode: "reply",
      to: ["list@example.com", "ana@work.example"],
      cc: [],
    },
    {
      name: "reply all copies the others, without the account itself",
      message: received,
      mode: "reply-all",
      to: ["ana@example.com"],
      cc: ["ben@example.com", "cy@example.com"],
    },
    {
      name: "reply all finds the account and duplicates in any case, keeping the first spelling",
      message: {
        ...received,
        from: [address("Ana@Example.com")],
        to: [address("STUDIO@example.com"), address("Ben@example.com"), address("ana@example.com")],
        cc: [address("ben@EXAMPLE.com"), address("cy@example.com")],
      },
      mode: "reply-all",
      to: ["Ana@Example.com"],
      cc: ["Ben@example.com", "cy@example.com"],
    },
    {
      name: "reply all keeps the member's other accounts, which may want the copy",
      message: { ...received, cc: [address("me@example.org")] },
      mode: "reply-all",
      to: ["ana@example.com"],
      cc: ["ben@example.com", "me@example.org"],
    },
    {
      name: "reply to a message the account sent goes to its recipients",
      message: {
        ...received,
        from: [address("studio@example.com")],
        to: [address("ben@example.com")],
        sent: true,
      },
      mode: "reply",
      to: ["ben@example.com"],
      cc: [],
    },
    {
      name: "reply all to a message the account sent keeps its To and Cc",
      message: {
        ...received,
        from: [address("studio@example.com")],
        to: [address("ben@example.com")],
        cc: [address("cy@example.com"), address("studio@example.com")],
        sent: true,
      },
      mode: "reply-all",
      to: ["ben@example.com"],
      cc: ["cy@example.com"],
    },
    {
      name: "a message with no sender starts with no recipients",
      message: { ...received, from: [], to: [], cc: [] },
      mode: "reply-all",
      to: [],
      cc: [],
    },
  ];

  it.each(cases)("$name", ({ message, mode, to, cc }) => {
    expect(replyDefaults(message, mode, accounts)).toEqual({
      connectedAccountId: studio.id,
      to,
      cc,
      bcc: [],
      body: "",
    });
  });

  it("sends from the receiving account only while the server lists it able to send", () => {
    // Usable, as when no Google client is set up: only allowedActions says.
    const cannotSend: ListedAccount = { ...studio, allowedActions: ["rename", "disconnect"] };
    expect(replyDefaults(received, "reply", [cannotSend, personal]).connectedAccountId).toBe(0);
    expect(replyDefaults(received, "reply", [personal]).connectedAccountId).toBe(0);
  });
});

describe("othersReceived", () => {
  it("is true when Reply all copies someone Reply leaves out", () => {
    expect(othersReceived(received, accounts)).toBe(true);
  });

  it("is false when only the account and the sender received it", () => {
    const direct = { ...received, to: [address("Studio@example.com")], cc: [] };
    expect(othersReceived(direct, accounts)).toBe(false);
  });

  it("is false for a message the account sent to one recipient", () => {
    const sent = { ...received, to: [address("ben@example.com")], cc: [], sent: true };
    expect(othersReceived(sent, accounts)).toBe(false);
  });
});
