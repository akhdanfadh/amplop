import { useEffect, type ReactNode } from "react";
import { CatchBoundary } from "@tanstack/react-router";
import { CrashScreen } from "@/components/route-states";
import { SetupGate } from "@/features/setup/gate";
import { watchTheme } from "@/lib/theme";

// AppShell renders children, the matched route, behind the installation
// gate and amplop's crash screen, and follows the theme while it is mounted.
//
// DESIGN: the root route's document shell places this in its <body>.
// Whole-app browser tests use this alone as the shell: they mount the app
// in a <div>, where React's own <html> and <body> would hang the page
// (src/test/app-router.ts says why).
export function AppShell({ children }: { children: ReactNode }) {
  // Follow the system theme and other tabs' choices for as long as the app runs.
  useEffect(() => watchTheme(), []);

  return (
    // The router's own boundaries sit inside SetupGate; this one shows
    // amplop's crash screen, not TanStack's, when the gate itself fails.
    <CatchBoundary getResetKey={() => "shell"} errorComponent={CrashScreen}>
      <SetupGate>{children}</SetupGate>
    </CatchBoundary>
  );
}
