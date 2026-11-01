import { describe, expect, it } from "vite-plus/test";
import type { SystemEmailSettings } from "@/api/generated/model";
import { validationMessage } from "@/api/validation";
import { checkedSave, keepsPassword, settingsSchema, settingsValues } from "./settings-schema";

const saved: SystemEmailSettings = {
  host: "smtp.example.com",
  port: 587,
  security: "starttls",
  username: "inbok@example.com",
  hasPassword: true,
  fromAddress: "inbok@example.com",
  fromName: "inbok",
  testedAt: null,
};
const values = settingsValues(saved);

// The paths and messages of the issues found in value, given saved settings.
function issuesWith(settings: SystemEmailSettings | undefined, value: typeof values) {
  const result = settingsSchema(settings).safeParse(value, { error: validationMessage });
  return (result.error?.issues ?? []).map(({ path, message }) => ({ path, message }));
}

function issues(value: typeof values) {
  return issuesWith(saved, value);
}

describe("settingsValues", () => {
  it("starts from the saved settings without a password", () => {
    expect(values).toEqual({
      host: "smtp.example.com",
      port: 587,
      security: "starttls",
      username: "inbok@example.com",
      password: "",
      fromAddress: "inbok@example.com",
      fromName: "inbok",
    });
  });

  it("starts a first save from STARTTLS defaults", () => {
    expect(settingsValues(undefined)).toMatchObject({ host: "", port: 587, security: "starttls" });
  });
});

describe("keepsPassword", () => {
  it("keeps the saved password for the same server and username", () => {
    expect(keepsPassword(saved, values)).toBe(true);
    expect(keepsPassword(saved, { ...values, host: " smtp.example.com " })).toBe(true);
  });

  it.each([
    ["host", { host: "smtp.other.example" }],
    ["port", { port: 465 }],
    ["username", { username: "other@example.com" }],
    ["username case", { username: "Inbok@example.com" }],
    ["blank username", { username: "  " }],
  ])("needs it again for another %s", (_, change) => {
    expect(keepsPassword(saved, { ...values, ...change })).toBe(false);
  });

  it("has nothing to keep without a saved password", () => {
    expect(keepsPassword(undefined, values)).toBe(false);
    expect(keepsPassword({ ...saved, hasPassword: false }, values)).toBe(false);
  });
});

describe("settingsSchema", () => {
  it("accepts saved settings that keep their password", () => {
    expect(issues(values)).toEqual([]);
  });

  it("accepts a server without sign-in", () => {
    expect(issues({ ...values, security: "none", username: "", port: 1025 })).toEqual([]);
  });

  it("reads a blank username as no sign-in", () => {
    expect(issues({ ...values, security: "none", username: "  ", port: 1025 })).toEqual([]);
  });

  it("accepts a new server with its password", () => {
    expect(issues({ ...values, host: "smtp.other.example", password: "app password" })).toEqual([]);
  });

  it("needs a password for a new server", () => {
    expect(issues({ ...values, host: "smtp.other.example" })).toEqual([
      { path: ["password"], message: "Enter the password for this server and username." },
    ]);
    expect(issuesWith(undefined, values)).toEqual([
      { path: ["password"], message: "Enter the password for this server and username." },
    ]);
  });

  it("refuses to sign in without encryption", () => {
    expect(issues({ ...values, security: "none" })).toEqual([
      { path: ["security"], message: "Choose TLS or STARTTLS to sign in with a username." },
    ]);
  });

  it("refuses a password without a username", () => {
    expect(issues({ ...values, username: "", password: "app password" })).toEqual([
      { path: ["username"], message: "Enter the username for this password." },
    ]);
  });

  it("reports a field limit and a rule between fields together", () => {
    expect(issues({ ...values, fromName: " ", security: "none" })).toEqual([
      { path: ["fromName"], message: "Can't be blank." },
      { path: ["security"], message: "Choose TLS or STARTTLS to sign in with a username." },
    ]);
  });

  it.each([
    ["host", { host: " " }, "Can't be blank."],
    ["port", { port: 0 }, "Enter a whole number from 1 to 65535."],
    ["port", { port: Number.NaN }, "Enter a whole number from 1 to 65535."],
    ["fromAddress", { fromAddress: "inbok" }, "Enter a valid value."],
    ["fromName", { fromName: "n".repeat(65) }, "Must be at most 64 characters."],
    ["password", { password: "p".repeat(256) }, "Must be at most 255 characters."],
  ])("applies the contract's limit on %s", (field, change, message) => {
    expect(issues({ ...values, ...change })).toContainEqual({ path: [field], message });
  });
});

describe("checkedSave", () => {
  it("confirms settings that match the form", () => {
    expect(checkedSave(saved, values)).toBe("saved");
  });

  it("compares the host, username, and From address as Go saves them", () => {
    expect(
      checkedSave(saved, {
        ...values,
        host: " smtp.example.com ",
        username: " inbok@example.com ",
        fromAddress: " inbok@example.com ",
      }),
    ).toBe("saved");
  });

  it("cannot confirm a typed password", () => {
    expect(checkedSave(saved, { ...values, password: "app password" })).toBe("passwordUnconfirmed");
  });

  it.each([
    ["nothing saved", undefined, values],
    ["another host", saved, { ...values, host: "smtp.other.example" }],
    ["another From name", saved, { ...values, fromName: "Changed" }],
  ])("reports %s as different", (_, settings, form) => {
    expect(checkedSave(settings, form)).toBe("different");
  });
});
