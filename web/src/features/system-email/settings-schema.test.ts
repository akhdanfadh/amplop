import { describe, expect, it } from "vite-plus/test";
import type { SystemEmailSettings } from "@/api/generated/model";
import { validationMessage } from "@/api/validation";
import { followedPort, settingsSchema, settingsValues } from "./settings-schema";

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
      security: "starttls",
      port: "587",
      username: "inbok@example.com",
      password: "",
      fromAddress: "inbok@example.com",
      fromName: "inbok",
    });
  });

  it("starts a first save from STARTTLS defaults", () => {
    expect(settingsValues(undefined)).toMatchObject({
      host: "",
      port: "587",
      security: "starttls",
    });
  });
});

describe("followedPort", () => {
  it("moves a mode's usual port to the new mode's", () => {
    expect(followedPort("tls", "587")).toBe("465");
    expect(followedPort("starttls", "465")).toBe("587");
    expect(followedPort("starttls", " 465 ")).toBe("587");
  });

  it("keeps a port the member typed", () => {
    expect(followedPort("tls", "2525")).toBe("2525");
    expect(followedPort("starttls", "")).toBe("");
  });

  it("keeps the port for a mode without a usual one", () => {
    expect(followedPort("none", "587")).toBe("587");
  });
});

describe("settingsSchema", () => {
  it("accepts saved settings that keep their password", () => {
    expect(issues(values)).toEqual([]);
  });

  it("accepts a server without sign-in", () => {
    expect(issues({ ...values, security: "none", username: "", port: "1025" })).toEqual([]);
  });

  it("reads a blank username as no sign-in", () => {
    expect(issues({ ...values, security: "none", username: "  ", port: "1025" })).toEqual([]);
  });

  it("sends the port as a number", () => {
    expect(
      settingsSchema(undefined).parse({ ...values, username: "", port: " 2525 " }),
    ).toMatchObject({
      port: 2525,
    });
  });

  it("keeps the saved password for the same server and username, as Go compares them", () => {
    expect(
      issues({ ...values, host: " smtp.example.com ", username: " inbok@example.com " }),
    ).toEqual([]);
  });

  it.each([
    ["host", { host: "smtp.other.example" }],
    ["port", { port: "465" }],
    ["username", { username: "other@example.com" }],
    ["username case", { username: "Inbok@example.com" }],
  ])("needs the password again for another %s", (_, change) => {
    expect(issues({ ...values, ...change })).toEqual([
      { path: ["password"], message: "Enter the password for this server and username." },
    ]);
  });

  it("has no saved password to keep when none is saved", () => {
    expect(issuesWith({ ...saved, hasPassword: false }, values)).toEqual([
      { path: ["password"], message: "Enter the password for this server and username." },
    ]);
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
    ["port", { port: "0" }, "Enter a whole number from 1 to 65535."],
    ["port", { port: "65536" }, "Enter a whole number from 1 to 65535."],
    ["port", { port: "" }, "Enter a whole number from 1 to 65535."],
    ["port", { port: "1e3" }, "Enter a whole number from 1 to 65535."],
    ["port", { port: "4.5" }, "Enter a whole number from 1 to 65535."],
    ["fromAddress", { fromAddress: "inbok" }, "Enter an email address, like name@example.com."],
    ["fromName", { fromName: "n".repeat(65) }, "Must be at most 64 characters."],
    ["password", { password: "p".repeat(256) }, "Must be at most 255 characters."],
  ])("applies the contract's limit on %s", (field, change, message) => {
    expect(issues({ ...values, ...change })).toContainEqual({ path: [field], message });
  });
});
