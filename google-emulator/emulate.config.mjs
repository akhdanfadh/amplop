// The development and trial stacks' Google: seeded Gmail mailboxes and one
// OAuth client, amplop's development one. Tests copy their own config over
// this file.

// The client is fixed here rather than read from amplop's settings, so a
// real Google client secret put in .env never reaches the emulator.
// .env.example carries the same two values.
const clientID = "amplop-dev.apps.googleusercontent.com";
const clientSecret = "amplop-dev-secret";

// amplop's callback: its public URL and one fixed path. The path must match
// the one amplop's API serves. URL resolution drops a trailing slash and
// spells the host as amplop's settings do, e.g. in lowercase, so the two sides
// send the same text.
const publicURL = process.env.AMPLOP_PUBLIC_URL;
if (!publicURL) throw new Error("AMPLOP_PUBLIC_URL is required");
const redirectURI = new URL("/api/v0/connected-accounts/google/callback", publicURL).href;

// One message in a seeded mailbox. Dates are fixed so every start shows
// the same inbox.
function message(user, id, fields) {
  return { id, user_email: user, to: user, label_ids: ["INBOX"], ...fields };
}

const alex = "alex@example.com";
const blair = "blair@example.com";
const casey = "casey@example.com";

export default {
  google: {
    // Without it the emulator answers Gmail calls without checking the
    // access token or its scopes, and revoking leaves the refresh token
    // working.
    strict_scopes: true,
    // Fixed IDs, as a Google account keeps its own, so a reconnect after
    // the emulator restarts finds the same account (see pnpm-workspace.yaml).
    users: [
      { email: alex, name: "Alex Rivera", uid: "100000000000000000001" },
      { email: blair, name: "Blair Chen", uid: "100000000000000000002" },
      { email: casey, name: "Casey Okafor", uid: "100000000000000000003" },
      // Google does not vouch for this address, so amplop must not connect
      // it.
      {
        email: "unverified@example.com",
        name: "Unverified Address",
        uid: "100000000000000000004",
        email_verified: false,
      },
    ],
    oauth_clients: [
      {
        client_id: clientID,
        client_secret: clientSecret,
        name: "amplop (development)",
        redirect_uris: [redirectURI],
      },
    ],
    // Blair's Work/Projects nests under Work, so the sidebar has a parent
    // label to fold, and a second account with labels.
    labels: [
      { id: "Label_receipts", user_email: alex, name: "Receipts" },
      { id: "Label_work", user_email: blair, name: "Work" },
      { id: "Label_projects", user_email: blair, name: "Work/Projects" },
    ],
    messages: [
      message(alex, "alex-1", {
        thread_id: "alex-t1",
        from: "Blair Chen <blair@example.com>",
        subject: "Lunch on Friday?",
        body_text: "Are you free for lunch on Friday? The new place on 5th opens this week.",
        label_ids: ["INBOX", "UNREAD", "CATEGORY_PERSONAL"],
        date: "2026-11-02T04:15:00.000Z",
        message_id: "<lunch-1@example.com>",
      }),
      message(alex, "alex-2", {
        thread_id: "alex-t1",
        from: alex,
        to: blair,
        subject: "Re: Lunch on Friday?",
        body_text: "Friday works. Noon?",
        label_ids: ["SENT"],
        date: "2026-11-02T05:02:00.000Z",
        message_id: "<lunch-2@example.com>",
        in_reply_to: "<lunch-1@example.com>",
        references: "<lunch-1@example.com>",
      }),
      message(alex, "alex-3", {
        from: "Orders <orders@shop.example>",
        subject: "Your order has shipped",
        body_html: "<p>Your order <b>#4821</b> is on its way.</p>",
        label_ids: ["INBOX", "Label_receipts", "CATEGORY_UPDATES"],
        date: "2026-11-01T09:30:00.000Z",
      }),
      message(blair, "blair-1", {
        from: "Casey Okafor <casey@example.com>",
        subject: "Slides for Monday",
        body_text: "Here is the outline for Monday's review. Comments welcome.",
        label_ids: ["INBOX", "UNREAD", "IMPORTANT"],
        date: "2026-11-03T02:45:00.000Z",
      }),
      message(blair, "blair-2", {
        from: "Newsletter <news@weekly.example>",
        subject: "This week in gardening",
        body_text: "Five plants that like the shade.",
        label_ids: ["INBOX", "CATEGORY_PROMOTIONS"],
        date: "2026-10-30T12:00:00.000Z",
      }),
      message(blair, "blair-3", {
        from: "Dana Park <dana@studio.example>",
        subject: "Quarterly planning notes",
        body_text: "Notes from today's planning session are attached to the shared folder.",
        label_ids: ["Label_work"],
        date: "2026-11-01T07:20:00.000Z",
      }),
      message(blair, "blair-4", {
        from: "Dana Park <dana@studio.example>",
        subject: "Website redesign: first drafts",
        body_text: "The first drafts are ready for review. Feedback by Thursday, please.",
        label_ids: ["INBOX", "UNREAD", "Label_projects"],
        date: "2026-11-02T10:05:00.000Z",
      }),
      message(casey, "casey-1", {
        from: "Alex Rivera <alex@example.com>",
        subject: "Welcome to the team",
        body_text: "Glad to have you here. Your laptop arrives on Tuesday.",
        label_ids: ["INBOX", "UNREAD"],
        date: "2026-11-04T01:10:00.000Z",
      }),
    ],
  },
};
