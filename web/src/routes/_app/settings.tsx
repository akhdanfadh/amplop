import type { ReactElement } from "react";
import { Outlet, createFileRoute } from "@tanstack/react-router";
import { Frame } from "@/components/frame";
import { SettingsNav } from "@/features/settings/nav";

// The settings frame: the settings sidebar beside one settings page.
export const Route = createFileRoute("/_app/settings")({
  // NOTE: only the loader, which this route lacks, is split off, so the
  // frame comes with the app, and a page still loading as the member moves
  // between pages shows its loading view inside the frame, not over the
  // whole window; the first load is one loading view over the window.
  codeSplitGroupings: [["loader"]],
  component: SettingsFrame,
});

function SettingsFrame(): ReactElement {
  return (
    <Frame label="Settings" nav={<SettingsNav />}>
      <Outlet />
    </Frame>
  );
}
