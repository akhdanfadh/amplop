import type { SystemEmailSettings } from "@/api/generated/model";

// SystemEmailState is whether amplop can send its own email, as far as the
// saved settings tell: "tested" when they passed a test message, "untested"
// when saved without one (or after a failed one, which clears the time),
// "none" when nothing is saved.
export type SystemEmailState = "tested" | "untested" | "none";

// systemEmailState returns the state of settings, the saved settings or
// undefined when none are saved. It reflects the last test, not whether
// mail sent since then arrived: the server records only tests.
export function systemEmailState(settings: SystemEmailSettings | undefined): SystemEmailState {
  if (!settings) return "none";
  return settings.testedAt ? "tested" : "untested";
}
