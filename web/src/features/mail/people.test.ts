import { describe, expect, it } from "vite-plus/test";
import { fullNames, initials, repliesElsewhere, senders } from "./people";

const ana = { name: "Ana Lima", address: "ana@example.com" };
const bo = { name: "Bo", address: "bo@example.com" };
const me = { name: "Sam Reyes", address: "Sam@Example.org" };
const bare = { name: "", address: "billing@shop.example" };

describe("senders", () => {
  it("names one sender in full", () => {
    expect(senders([ana], "sam@example.org")).toEqual(["Ana Lima"]);
    expect(senders([bare], undefined)).toEqual(["billing@shop.example"]);
  });

  it("names several by first name, and the account as me", () => {
    expect(senders([ana, me, bo], "sam@example.org")).toEqual(["Ana", "me", "Bo"]);
  });

  it("names an entry without a name by its address's first part", () => {
    expect(senders([bare, ana], undefined)).toEqual(["billing", "Ana"]);
  });

  it("calls a lone message from the account me", () => {
    expect(senders([me], "SAM@example.org")).toEqual(["me"]);
  });
});

describe("initials", () => {
  it("takes the first and last words' first letters", () => {
    expect(initials({ name: "Ana Maria Lima", address: "" })).toBe("AL");
    expect(initials(bo)).toBe("B");
  });

  it("falls back to the address", () => {
    expect(initials(bare)).toBe("B");
  });

  it("is empty without a sender", () => {
    expect(initials(undefined)).toBe("");
    expect(initials({ name: " ", address: "" })).toBe("");
  });

  it("keeps a letter outside the basic plane whole", () => {
    expect(initials({ name: "𝓐nna", address: "" })).toBe("𝓐");
  });

  it("skips words that do not start with a letter", () => {
    expect(initials({ name: "👩🏽‍💻 Priya", address: "" })).toBe("P");
    expect(initials({ name: "🎉🎉", address: "party@example.com" })).toBe("P");
    expect(initials({ name: "Đặng Thị Ngọc Hân", address: "" })).toBe("ĐH");
  });
});

describe("fullNames", () => {
  it("names each in full, and the account as me", () => {
    expect(fullNames([ana, bare, me], "sam@example.org")).toBe(
      "Ana Lima, billing@shop.example, me",
    );
  });
});

describe("repliesElsewhere", () => {
  it("calls out a Reply-To at a domain no sender is at", () => {
    expect(repliesElsewhere([{ name: "", address: "help@other.example" }], [ana])).toBe(true);
  });

  it("lets one at a sender's own domain pass, in any case", () => {
    expect(repliesElsewhere([{ name: "", address: "help@EXAMPLE.com" }], [ana])).toBe(false);
    expect(repliesElsewhere([], [ana])).toBe(false);
  });

  it("reads the domain after the last @, as a quoted local part may hold one", () => {
    expect(repliesElsewhere([{ name: "", address: '"a@other.example"@example.com' }], [ana])).toBe(
      false,
    );
  });

  it("ignores an entry that is no address, e.g. a group's name", () => {
    expect(repliesElsewhere([{ name: "Undisclosed recipients", address: "" }], [ana])).toBe(false);
  });
});
