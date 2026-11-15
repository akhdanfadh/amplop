import type { SystemEmailSettings } from "@/api/generated/model";
import type { LeaveWarningText } from "@/components/leave-warning";

type Security = SystemEmailSettings["security"];

// The page's lead: what system email is.
export const systemEmailLead =
  "The mail server inbok sends its own messages through, like sign-in codes. Members' mailboxes are separate.";

// The settings' section: its title and what it is for.
export const mailServerTitle = "Mail server";
export const mailServerAbout = "The SMTP server and sender inbok's own messages go out with.";

// The test's section: its title and what it is for.
export const testTitle = "Test message";
export const testAbout = "Sends a message with the saved settings, to check that they work.";

// The badge beside the title: whether inbok can send its own email.
export const badgeText = {
  tested: "Tested",
  // True after a failed test as before any: Go clears the tested time
  // when a test fails.
  untested: "Not confirmed",
  none: "Not set up",
};

// The summary's names for the saved settings, and its words for what is
// not set.
export const summaryText = {
  server: "Server",
  port: "Port",
  security: "Security",
  username: "Username",
  password: "Password",
  from: "From",
  noUsername: "None, no sign-in",
  noSecurity: "None, not encrypted",
  passwordSaved: "Saved",
  noPassword: "None",
};

// The security modes, as the Security choice offers them.
export const securityOptions = [
  { value: "tls", label: "TLS", description: "Encrypted from the start." },
  {
    value: "starttls",
    label: "STARTTLS",
    description: "Switches to an encrypted connection before signing in.",
  },
  {
    value: "none",
    label: "None",
    description: "Not encrypted, so it can't sign in. For a local test server only.",
  },
] as const satisfies { value: Security; label: string; description: string }[];

// securityLabel returns how security is named, e.g. "STARTTLS".
export function securityLabel(security: Security): string {
  return securityOptions.find((option) => option.value === security)?.label ?? security;
}

// The form's groups: a title and what each holds.
export const groupText = {
  connection: { title: "Connection", about: "From your email provider's SMTP settings." },
  signIn: { title: "Sign-in", about: "The account inbok signs in to the mail server with." },
  sender: { title: "Sender", about: "Who inbok's messages come from." },
};

// The form's fields: each label and the line before its control.
export const fieldText = {
  host: { label: "Server", description: "The SMTP host name, like smtp.example.com." },
  security: { label: "Security" },
  port: { label: "Port" },
  username: {
    label: "Username",
    description: "Leave empty if the mail server doesn't need a sign-in.",
  },
  password: {
    label: "Password",
    toggle: "mail server password",
    kept: "Leave empty to keep the saved password. Changing the server, port, or username needs it again.",
    new: "The password for this username. Some providers call it an app password.",
  },
  fromAddress: {
    label: "From address",
    description: "The address inbok sends from. Your provider must allow this account to use it.",
  },
  fromName: { label: "From name", description: "The sender name recipients see." },
};

// portHint names port as the usual port for security, or, for a mode
// without one (port undefined), says the test server picks it. state says
// whether the field holds the port because it "changed" with Security just
// now, or as the member "kept" it.
export function portHint(
  security: Security,
  port: string | undefined,
  state: "changed" | "kept",
): string {
  if (!port) return "The port your test server listens on.";
  const mode = securityLabel(security);
  if (state === "changed") return `Changed to ${port}, the usual port for ${mode}.`;
  return `The usual port for ${mode} is ${port}.`;
}

// The form's buttons and what they say while they work.
export const formText = {
  edit: "Edit settings",
  save: "Save settings",
  saving: "Saving settings...",
  cancel: "Cancel",
  untestedNote: "Saving marks the settings as untested until a test message passes again.",
};

// What the section says once a save closed the form.
export const savedNotice = "Settings saved. Send a test message to check them.";

// What a save whose answer was lost says, after the cause. Saving replaces
// every setting, so saving the same values again is safe (ADR022).
export const lostSave = "We couldn't confirm the settings were saved. Save again to make sure.";

// Leaving with changes in the open form loses them.
export const leaveText: LeaveWarningText = {
  title: "Discard your changes?",
  description: "Your changes to the system email settings aren't saved.",
  stay: "Keep editing",
  leave: "Discard changes",
  leaveVariant: "destructive",
};

// The test form's words.
export const testText = {
  recipient: { label: "Recipient", description: "Where to send the test message." },
  send: "Send test message",
  sending: "Sending test message...",
  noSettings: "Save the settings before sending a test message.",
  untested: "These settings haven't passed a test yet.",
  passed: "Last test passed",
  waiting: "Save or cancel your changes to send a test.",
};

// The read's failure, before its cause.
export const readFailed = "Could not check the system email settings.";
