import { describe, expect, it } from "vite-plus/test";
import { codesLeft } from "./text";

describe("codesLeft", () => {
  it.each([
    // One row a branch. Three is the fewest that says nothing more, two the
    // most that asks for new ones.
    {
      name: "enough codes, with nothing more",
      left: 3,
      waiting: false,
      words: "3 of 10 backup codes left.",
    },
    {
      name: "few codes, asking to replace them",
      left: 2,
      waiting: false,
      words:
        "2 of 10 backup codes left. Replace them so a lost authenticator doesn't lock you out.",
    },
    {
      name: "no codes, asking to make new ones",
      left: 0,
      waiting: false,
      words: "No backup codes left. Make new ones so a lost authenticator doesn't lock you out.",
    },
    {
      name: "new codes not saved yet, while the old ones still work",
      left: 2,
      waiting: true,
      words:
        "2 of 10 backup codes left. New codes were made but not saved yet; your old codes still work.",
    },
    {
      name: "new codes not saved yet, with no old one left",
      left: 0,
      waiting: true,
      words: "No backup codes left. New codes were made but not saved yet.",
    },
  ])("words $name", ({ left, waiting, words }) => {
    expect(codesLeft({ backupCodesLeft: left, backupCodesWaiting: waiting })).toBe(words);
  });
});
