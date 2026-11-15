import type { Step } from "./reminder";

// The landmark that holds the setup reminders, so a screen reader can find
// them.
export const remindersLabel = "Setup reminders";

// The setup reminder's words for each step: what is not done and what that
// stops, then the link to where it is done.
export const reminderText = {
  setUpSystemEmail: {
    notice:
      "Finish setting up system email: until it works, members can't reset a password or verify their address.",
    link: "Set up system email",
  },
  // The link already says what to do, so the notice says what waits for it:
  // the server opens registration only with tested settings.
  testSystemEmail: {
    notice: "Finish setting up system email: until a test message passes, registration can't open.",
    link: "Send a test message",
  },
  verifyEmail: {
    notice: "Your email address isn't verified, so a forgotten password can't be reset by email.",
    link: "Verify it in Security",
  },
} satisfies Record<Step, { notice: string; link: string }>;
