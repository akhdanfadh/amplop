import { describe, expect, it } from "vite-plus/test";
import { initials, senders } from "./people";

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
