import type { SystemEmailTestMessageResult } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";

type Reason = NonNullable<SystemEmailTestMessageResult["reason"]>;

// Go names the step that failed, not its cause, so each message points at
// the settings that step uses.
const failures: Record<Reason, string> = {
  connect: "Couldn't connect to the mail server. Check the host and port.",
  tls: "Couldn't set up an encrypted connection. Check the security setting and port.",
  auth: "The mail server rejected the sign-in. Check the username and password.",
  sender: "The mail server refused the message from this sender. Check the From address.",
  recipient: "The mail server refused the recipient. Check the recipient address.",
  timeout: "The mail server stopped responding. Check the host and port, then try again.",
};
const failed = "The test message failed. Check the settings and try again.";
const lostResult = "We couldn't get the test result. Send another test to check the settings.";

// testResultMessage words a test result received from Go.
export function testResultMessage(result: SystemEmailTestMessageResult, recipient: string): string {
  if (result.status === "accepted")
    return `The mail server accepted the test message. Check the inbox of ${recipient}.`;
  return (result.reason && failures[result.reason]) || failed;
}

// testErrorMessage words a failed test request.
export function testErrorMessage(err: ApiError): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  // A lost response may hide a sent message, but sending another only sends
  // another test, so no check is needed first.
  return uncertainMessage(err, lostResult);
}
