import { expect } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import type {
  CurrentSession,
  ListedSessions,
  TotpEnrollment,
  TotpStatus,
  WaitingBackupCodes,
} from "@/api/generated/model";
import { data, fakeServer, memberSession, problem, type Server } from "@/test/server";

// What the tests of two-factor authentication's pages share: a fake server
// that keeps a two-factor status as inbok's does, and the setup's steps.

const created = 201;
const noContent = 204;
const unprocessable = 422;

// The member's password, which starts a setup.
export const password = "correct horse battery staple";

const secret = "JBSWY3DPEHPK3PXP";

// The backup codes every code check and replacement answers.
export const codes = [
  "AAAA-BBBB1",
  "AAAA-BBBB2",
  "AAAA-BBBB3",
  "AAAA-BBBB4",
  "AAAA-BBBB5",
  "AAAA-BBBB6",
  "AAAA-BBBB7",
  "AAAA-BBBB8",
  "AAAA-BBBB9",
  "AAAA-BBBB0",
];

// What Security says once two-factor authentication turned on.
export const onNotice = "Two-factor authentication is on. Your other browsers are signed out.";

const offStatus: TotpStatus = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };
const onStatus: TotpStatus = { status: "on", backupCodesLeft: 10, backupCodesWaiting: false };

const enrollment: TotpEnrollment = {
  secret,
  uri: `otpauth://totp/inbok:ana%40example.com?secret=${secret}&issuer=inbok`,
};

const thisBrowser: ListedSessions["sessions"][number] = {
  id: "0193c8a0-0000-7000-8000-000000000001",
  browser: "Firefox",
  os: "Linux",
  signedInAt: "2026-11-01T10:00:00Z",
  signedInFrom: "203.0.113.7",
  lastActiveAt: "2026-11-01T10:00:00Z",
  current: true,
};

const otherBrowser: ListedSessions["sessions"][number] = {
  id: "0193c8a0-0000-7000-8000-000000000002",
  browser: "Safari",
  os: "iOS",
  signedInAt: "2026-11-02T10:00:00Z",
  signedInFrom: "203.0.113.8",
  lastActiveAt: "2026-11-02T10:00:00Z",
  current: false,
};

// waiting returns the answer that shows the backup codes, with token to
// finish with.
function waiting(token: string): WaitingBackupCodes {
  return { backupCodes: codes, token };
}

// TotpApi is a fake inbok whose member, ana@example.com, has the
// two-factor status totp, which its writes change, and role, which the
// session read answers: a test sets either to change it. server answers
// the requests; a test adds a route of its own there, e.g. a refusal.
export type TotpApi = {
  server: Server;
  totp: TotpStatus;
  role: CurrentSession["role"];
};

// totpApi returns a TotpApi for a member with role whose two-factor
// status is totp. Its server answers two-factor authentication's
// operations, the session read, with a session that may only set
// two-factor up while an administrator's is not on, the signed-in
// browsers, and sign-out, which ends the session.
export function totpApi(totp: TotpStatus, role: CurrentSession["role"] = "member"): TotpApi {
  const server = fakeServer({ ...memberSession, role });
  const api: TotpApi = { server, totp, role };

  server.on("GET", "/api/v0/sessions/current", () =>
    data({
      ...memberSession,
      role: api.role,
      // An administrator may only set it up until it is on.
      totpRequired: api.role === "admin" && api.totp.status !== "on",
    } satisfies CurrentSession),
  );
  server.on("DELETE", "/api/v0/sessions/current", () => {
    server.session = undefined;
    return new Response(null, { status: noContent });
  });
  server.on("GET", "/api/v0/sessions", () =>
    data({
      // Turning on signs out the other browsers.
      sessions: api.totp.status === "on" ? [thisBrowser] : [thisBrowser, otherBrowser],
    } satisfies ListedSessions),
  );

  server.on("GET", "/api/v0/totp", () => data(api.totp));
  server.on("POST", "/api/v0/totp", ({ body }) => {
    if (
      !(typeof body === "object" && body !== null && "password" in body) ||
      body.password !== password
    )
      return problem(unprocessable, "/api/v0/problems/invalid-password", {
        title: "Incorrect password",
        detail: "Enter your current password.",
        errors: [{ location: "body.password", message: "Enter your current password." }],
      });
    api.totp = { status: "pending", backupCodesLeft: 0, backupCodesWaiting: false };
    return data(enrollment, created);
  });
  server.on("DELETE", "/api/v0/totp/enrollment", () => {
    api.totp = offStatus;
    return data(api.totp);
  });
  server.on("POST", "/api/v0/totp/enrollment/code", () => {
    api.totp = { ...api.totp, backupCodesWaiting: true };
    return data(waiting("setup-token"));
  });
  server.on("POST", "/api/v0/totp/enrollment/finish", () => {
    api.totp = onStatus;
    return data(api.totp);
  });
  server.on("POST", "/api/v0/totp/backup-codes", () => {
    api.totp = { ...api.totp, backupCodesWaiting: true };
    return data(waiting("replace-token"));
  });
  server.on("POST", "/api/v0/totp/backup-codes/waiting", () => data(waiting("shown-again-token")));
  server.on("POST", "/api/v0/totp/backup-codes/finish", () => {
    api.totp = onStatus;
    return data(api.totp);
  });
  server.on("POST", "/api/v0/totp/disable", () => {
    api.totp = offStatus;
    return data(api.totp);
  });
  return api;
}

// off returns a TotpApi where two-factor authentication is off, for a
// member with role.
export function off(role?: CurrentSession["role"]): TotpApi {
  return totpApi(offStatus, role);
}

// on returns a TotpApi where two-factor authentication is on with ten
// backup codes, for a member with role.
export function on(role?: CurrentSession["role"]): TotpApi {
  return totpApi(onStatus, role);
}

// sent returns the requests api's server got so far, in order, each as
// "METHOD /path".
export function sent(api: TotpApi): string[] {
  return api.server.requests.map((asked) => `${asked.method} ${asked.url.pathname}`);
}

export const passwordField = () => page.getByLabelText("Password", { exact: true });
export const codeField = () => page.getByRole("textbox", { name: "Authenticator code" });

// endStepEntrance ends the entrance of the step now on screen, 200 ms of
// sliding in (stepEnter, components/step-change.ts), so the next press
// does not wait it out: Playwright presses only what has stopped moving.
function endStepEntrance(): void {
  for (const animation of document.getAnimations()) {
    if (animation instanceof CSSAnimation && animation.animationName === "enter")
      animation.finish();
  }
}

// toQr enters the password on the setup's password step.
export async function toQr(): Promise<void> {
  await expect.element(passwordField()).toHaveFocus();
  await userEvent.fill(passwordField(), password);
  await userEvent.click(page.getByRole("button", { name: "Continue" }));
  await expect.element(page.getByRole("heading", { name: /Scan this QR code/ })).toHaveFocus();
  endStepEntrance();
}

// toCodes checks the first code on the QR step.
export async function toCodes(): Promise<void> {
  await userEvent.click(codeField());
  await userEvent.keyboard("123456");
  await userEvent.click(page.getByRole("button", { name: "Continue" }));
  await expect
    .element(page.getByRole("heading", { level: 2, name: "Save your backup codes" }))
    .toHaveFocus();
  endStepEntrance();
}
