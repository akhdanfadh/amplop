import * as z from "zod";
import { beforeAll, describe, expect, it } from "vite-plus/test";
import { NewProbeEvent, NewSession } from "./generated/model";
import { emailAddress, validationMessage } from "./validation";

const valid = { note: "n", amount: 1, highlighted: false, tag: null };

beforeAll(() => {
  z.config({ customError: validationMessage });
});

// The messages Zod reports for value against schema.
function messages(schema: z.ZodType, value: unknown) {
  return schema.safeParse(value).error?.issues.map((i) => i.message);
}

describe("validationMessage", () => {
  it.each([
    ["a blank note", { note: "   " }, "Can't be blank."],
    ["an empty note", { note: "" }, "Can't be blank."],
    ["a note over 120 characters", { note: "x".repeat(121) }, "Must be at most 120 characters."],
    ["an amount below 1", { amount: -98 }, "Enter a whole number from 1 to 10."],
    ["an amount above 10", { amount: 11 }, "Enter a whole number from 1 to 10."],
    ["an emptied amount", { amount: Number.NaN }, "Enter a whole number from 1 to 10."],
    ["a fractional amount", { amount: 1.5 }, "Enter a whole number from 1 to 10."],
    ["an empty tag", { tag: "" }, "Must be 1 to 40 characters."],
    ["a tag over 40 characters", { tag: "t".repeat(41) }, "Must be 1 to 40 characters."],
    ["a missing note", { note: undefined }, "Enter a valid value."],
    ["a missing field", { highlighted: undefined }, "Enter a valid value."],
  ])("states the rule for %s", (_case, change, message) => {
    expect(messages(NewProbeEvent, { ...valid, ...change })).toEqual([message]);
  });

  it.each([
    ["a whole number with a minimum", z.int().min(1), 0, "Enter a whole number no less than 1."],
    ["a whole number with a maximum", z.int().max(10), 11, "Enter a whole number no more than 10."],
    ["a whole number without limits", z.int(), 1.5, "Enter a whole number."],
    ["a number without limits", z.number(), Number.NaN, "Enter a number."],
    ["text with a minimum length", z.string().min(3), "ab", "Must be at least 3 characters."],
    ["text of at most one character", z.string().max(1), "ab", "Must be at most 1 character."],
    [
      "a list over its limit",
      z.array(z.string()).max(100),
      Array(101).fill("a"),
      "Must have at most 100 items.",
    ],
    [
      "a list of at most one item",
      z.array(z.string()).max(1),
      ["a", "b"],
      "Must have at most 1 item.",
    ],
  ])("states the rule for %s", (_case, schema, value, message) => {
    expect(messages(schema, value)).toEqual([message]);
  });
});

describe("emailAddress", () => {
  const email = emailAddress(NewSession.shape.email);

  // The server takes what Go's net/mail does (backend/internal/emailaddress),
  // including internationalized addresses (RFC 6531) and names without a dot.
  it.each([
    ["a plain address", "ana@example.com"],
    ["capitals and a tag", "Ana.B+tag@Example.COM"],
    ["an accented local part", "josé@example.com"],
    ["an internationalized domain", "ana@münchen.de"],
    ["a punycode top-level domain", "x@e.xn--p1ai"],
    ["a domain without a dot", "ana@localhost"],
    ["an apostrophe", "o'brien@example.com"],
  ])("accepts %s", (_case, value) => {
    expect(messages(email, value)).toBeUndefined();
  });

  it.each([
    ["no @", "ana.example.com"],
    ["nothing before @", "@example.com"],
    ["nothing after @", "ana@"],
    ["a space", "ana @example.com"],
    ["two @", "ana@b@example.com"],
    ["a quoted local part", '"ana"@example.com'],
  ])("refuses %s", (_case, value) => {
    expect(messages(email, value)).toEqual(["Enter an email address, like name@example.com."]);
  });

  it("gives an empty field one message", () => {
    expect(messages(email, "")).toEqual(["Enter an email address, like name@example.com."]);
  });

  it("keeps the contract's length limits for an address that is otherwise one", () => {
    const long = `${"a".repeat(64)}@${"b".repeat(190)}.com`;

    expect(messages(email, long)).toEqual(["Must be 3 to 254 characters."]);
  });
});
