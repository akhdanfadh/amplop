import { describe, expect, it } from "vite-plus/test";
import { portHint } from "./text";

describe("portHint", () => {
  it.each([
    [
      "a port Security just changed",
      "starttls",
      "587",
      "changed",
      "Changed to 587, the usual port for STARTTLS.",
    ],
    ["a port the member kept", "tls", "465", "kept", "The usual port for TLS is 465."],
    [
      "a mode without a usual port",
      "none",
      undefined,
      "kept",
      "The port your test server listens on.",
    ],
    // Security just changed to a mode that has no port to change to.
    [
      "no port after a change",
      "none",
      undefined,
      "changed",
      "The port your test server listens on.",
    ],
  ] as const)("words %s", (_, security, port, state, hint) => {
    expect(portHint(security, port, state)).toBe(hint);
  });
});
