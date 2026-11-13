import { linkOptions } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { forgetMailHref, isViewPlace, mailHref, rememberMailHref } from "./places";

describe("isViewPlace", () => {
  it("knows the sidebar's six views", () => {
    for (const place of ["inbox", "starred", "sent", "all", "spam", "trash"]) {
      expect(isViewPlace(place)).toBe(true);
    }
  });

  it("refuses anything else", () => {
    expect(isViewPlace("outbox")).toBe(false);
    expect(isViewPlace("Inbox")).toBe(false);
  });
});

describe("mailHref", () => {
  it("opens the Inbox until a page in mail is remembered", () => {
    expect(mailHref()).toEqual({ to: "/mail/$place", params: { place: "inbox" } });
  });

  it("opens the last page remembered", () => {
    const outbox = linkOptions({ to: "/mail/outbox/$id", params: { id: "a" } });
    rememberMailHref(outbox);
    expect(mailHref()).toBe(outbox);
  });

  it("opens the Inbox again once forgotten, e.g. at sign-out", () => {
    rememberMailHref(linkOptions({ to: "/mail/outbox/$id", params: { id: "a" } }));
    forgetMailHref();
    expect(mailHref()).toEqual({ to: "/mail/$place", params: { place: "inbox" } });
  });
});
