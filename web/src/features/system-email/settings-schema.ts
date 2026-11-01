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

// keepsPassword reports whether saving values without a password keeps the
// saved one, as Go decides: only for the same host, port, and username.
export function keepsPassword(saved: SystemEmailSettings | undefined, values: SettingsValues) {
  return Boolean(
    saved?.hasPassword &&
    values.username &&
    // Go trims the host before comparing; it keeps the username as entered.
    saved.host === values.host.trim() &&
    saved.port === values.port &&
    saved.username === values.username,
  );
}

// settingsSchema checks the contract's limits and the rules Go applies
// between fields, given the settings saved now.
export function settingsSchema(saved: SystemEmailSettings | undefined) {
  return NewSystemEmailSettings.required()
    .refine((value) => value.security !== "none" || !value.username, {
      path: ["security"],
      error: "Choose TLS or STARTTLS to sign in with a username.",
    })
    .refine((value) => value.username || !value.password, {
      path: ["username"],
      error: "Enter the username for this password.",
    })
    .refine((value) => !value.username || value.password || keepsPassword(saved, value), {
      path: ["password"],
      error: "Enter the password for this server and username.",
    });
}

// checkedSave compares the settings read after an uncertain save with the
// form.
export function checkedSave(
  saved: SystemEmailSettings | undefined,
  values: SettingsValues,
): "saved" | "passwordUnconfirmed" | "different" {
  const matches =
    saved !== undefined &&
    saved.host === values.host.trim() &&
    saved.port === values.port &&
    saved.security === values.security &&
    saved.username === values.username &&
    saved.fromAddress === values.fromAddress.trim() &&
    saved.fromName === values.fromName;
  if (!matches) return "different";
  // The password is never read back, so a typed one stays unconfirmed.
  return values.password ? "passwordUnconfirmed" : "saved";
}
