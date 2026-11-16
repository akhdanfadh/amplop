import { describe, expect, it } from "vite-plus/test";
import { laidOut } from "./slots";

describe("laidOut", () => {
  it("gives each slot its first action offered, in slot order, and keeps Report spam in More", () => {
    expect(laidOut(["archive", "trash", "spam", "unread", "star"])).toEqual({
      buttons: [
        { slot: "move", action: "archive" },
        { slot: "delete", action: "trash" },
        { slot: "mark", action: "unread" },
        { slot: "star", action: "star" },
      ],
      more: ["spam"],
    });
  });

  it("puts a slot's other action in More where mixed rows offer both", () => {
    expect(laidOut(["archive", "unarchive", "read", "unread", "star", "unstar"])).toEqual({
      buttons: [
        { slot: "move", action: "archive" },
        { slot: "mark", action: "read" },
        { slot: "star", action: "star" },
      ],
      more: ["unarchive", "unread", "unstar"],
    });
  });
});
