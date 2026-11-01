import { NewSystemEmailSettings, type SystemEmailSettings } from "@/api/generated/model";

// The form holds exactly the request body, so it is sent as is.
export type SettingsValues = Required<NewSystemEmailSettings>;

const newSettings: SettingsValues = {
  host: "",
  port: 587,
  security: "starttls",
  username: "",
  password: "",
  fromAddress: "",
  fromName: "inbok",
};

// settingsValues returns the form's starting values: the saved settings with
// an empty password, or defaults for a first save.
export function settingsValues(saved: SystemEmailSettings | undefined): SettingsValues {
  if (!saved) return newSettings;
  const { host, port, security, username, fromAddress, fromName } = saved;
  return { host, port, security, username, password: "", fromAddress, fromName };
}

// sameAccount reports whether values name the saved host, port, and
// username, compared as Go saves them.
function sameAccount(saved: SystemEmailSettings, values: SettingsValues): boolean {
  // Go trims the host and username before saving.
  return (
    saved.host === values.host.trim() &&
    saved.port === values.port &&
    saved.username === values.username.trim()
  );
}

// keepsPassword reports whether saving values without a password keeps the
// saved one, as Go decides: only for the same host, port, and username.
export function keepsPassword(saved: SystemEmailSettings | undefined, values: SettingsValues) {
  return Boolean(saved?.hasPassword && values.username.trim() && sameAccount(saved, values));
}

// settingsSchema checks the contract's limits and the rules Go applies
// between fields, given the settings saved now.
export function settingsSchema(saved: SystemEmailSettings | undefined) {
  return (
    NewSystemEmailSettings.required()
      // Go saves a blank username as none, so these rules read it trimmed.
      .refine((value) => value.security !== "none" || !value.username.trim(), {
        path: ["security"],
        error: "Choose TLS or STARTTLS to sign in with a username.",
      })
      .refine((value) => value.username.trim() || !value.password, {
        path: ["username"],
        error: "Enter the username for this password.",
      })
      .refine((value) => !value.username.trim() || value.password || keepsPassword(saved, value), {
        path: ["password"],
        error: "Enter the password for this server and username.",
      })
  );
}

// savedAddress returns address as Go saves it: trimmed, with the letters A to
// Z lowercased and other letters as typed (ADR025).
function savedAddress(address: string): string {
  return address.trim().replace(/[A-Z]/g, (letter) => letter.toLowerCase());
}

// checkedSave compares the settings read after an uncertain save with the
// form.
export function checkedSave(
  saved: SystemEmailSettings | undefined,
  values: SettingsValues,
): "saved" | "passwordUnconfirmed" | "different" {
  const matches =
    saved !== undefined &&
    sameAccount(saved, values) &&
    saved.security === values.security &&
    saved.fromAddress === savedAddress(values.fromAddress) &&
    saved.fromName === values.fromName;
  if (!matches) return "different";
  // The password is never read back, so a typed one stays unconfirmed.
  return values.password ? "passwordUnconfirmed" : "saved";
}
