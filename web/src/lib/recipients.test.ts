import { describe, expect, it } from "vite-plus/test";
import { parseRecipients } from "./recipients";

describe("parseRecipients", () => {
  it.each([
    ["", []],
    ["  ", []],
    ["ana@example.com", ["ana@example.com"]],
    ["ana@example.com, bo@example.com", ["ana@example.com", "bo@example.com"]],
    ["ana@example.com;bo@example.com", ["ana@example.com", "bo@example.com"]],
    [" ana@example.com  bo@example.com ,, ", ["ana@example.com", "bo@example.com"]],
    ["ana@example.com,\nbo@example.com", ["ana@example.com", "bo@example.com"]],
    ["Ana Lopez <ana@example.com>, bo@example.com", ["ana@example.com", "bo@example.com"]],
    ['"Lopez, Ana" <ana@example.com>', ["ana@example.com"]],
    [
      '"Lopez, Ana" <ana@example.com>; "Bo" <bo@example.com>',
      ["ana@example.com", "bo@example.com"],
    ],
    // Quoted text with no <address> is kept, to show as not an address.
    ['"copy@example.com"', ['"copy@example.com"']],
    ['"copy@example.com", bo@example.com', ['"copy@example.com"', "bo@example.com"]],
    ['"Lopez, Ana"', ['"Lopez, Ana"']],
  ])("splits %j", (text, addresses) => {
    expect(parseRecipients(text)).toEqual(addresses);
  });
});
