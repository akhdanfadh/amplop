import * as z from "zod";
import { NewSystemEmailSettings, type SystemEmailSettings } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";

type Security = SystemEmailSettings["security"];

// The form's values: the request body, with the port as typed, since a
// port is a number that names something, typed in a text box.
export type SettingsValues = Omit<Required<NewSystemEmailSettings>, "port"> & { port: string };

const newSettings: SettingsValues = {
  host: "",
  security: "starttls",
  port: "587",
  username: "",
  password: "",
  fromAddress: "",
  fromName: "inbok",
};

// The port each mode usually uses (RFC 8314 section 3.3 for TLS, RFC 6409
// for STARTTLS submission). None has none: a test server picks its own.
export const usualPort: Record<Security, string | undefined> = {
  tls: "465",
  starttls: "587",
  none: undefined,
};

// settingsValues returns the form's starting values: the saved settings with
// an empty password, or defaults for a first save.
export function settingsValues(saved: SystemEmailSettings | undefined): SettingsValues {
  if (!saved) return newSettings;
  const { host, port, security, username, fromAddress, fromName } = saved;
  return { host, security, port: String(port), username, password: "", fromAddress, fromName };
}

// portNumber reads a typed port: digits only, so "1e3" or "0x1f" are not
// numbers here though Number() reads them; anything else is NaN, which
// the contract's number check words as its rule.
function portNumber(text: string): number {
  const trimmed = text.trim();
  return /^\d+$/.test(trimmed) ? Number(trimmed) : Number.NaN;
}

// keepsSaved reports whether saving body without a password keeps the
// saved password, as Go decides: only for the same host, port, and
// username.
function keepsSaved(
  saved: SystemEmailSettings | undefined,
  body: { host: string; port: number; username: string },
): boolean {
  // Go trims the host and username before saving.
  return Boolean(
    saved?.hasPassword &&
    body.username.trim() &&
    saved.host === body.host.trim() &&
    saved.port === body.port &&
    saved.username === body.username.trim(),
  );
}

// settingsSchema checks the contract's limits and the rules Go applies
// between fields, given the settings saved now. Parsing returns the request
// body, the port a number.
export function settingsSchema(saved: SystemEmailSettings | undefined) {
  return (
    NewSystemEmailSettings.required()
      .extend({
        port: z.string().transform(portNumber).pipe(NewSystemEmailSettings.shape.port),
        fromAddress: emailAddress(NewSystemEmailSettings.shape.fromAddress),
      })
      // Go saves a blank username as none, so these rules read it trimmed.
      .refine((value) => value.security !== "none" || !value.username.trim(), {
        path: ["security"],
        error: "Choose TLS or STARTTLS to sign in with a username.",
      })
      .refine((value) => value.username.trim() || !value.password, {
        path: ["username"],
        error: "Enter the username for this password.",
      })
      .refine((value) => !value.username.trim() || value.password || keepsSaved(saved, value), {
        path: ["password"],
        error: "Enter the password for this server and username.",
      })
  );
}
