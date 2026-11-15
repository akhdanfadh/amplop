import { useEffect, useState, type ReactElement } from "react";
import { useSetup } from "./gate";
import { setupDone } from "./text";

// SetupDoneNotice tells the operator who just created the first
// administrator, in this browser, to remove the setup token. It is the
// first page after setup: the two-factor setup card, or sign-in when setup's
// sign-in failed. It reports a result, so it looks like a settings page's
// notice (DESIGN.md "Settings and signed-out pages").
export function SetupDoneNotice(): ReactElement {
  const { completedHere } = useSetup();
  // A screen reader announces a live region's change, not what it already
  // holds when it appears (MDN, "ARIA live regions"), and the card's
  // password field takes focus from the title, so the notice fills its
  // region a frame after the region is drawn.
  const [listening, setListening] = useState(false);

  useEffect(() => {
    const frame = requestAnimationFrame(() => setListening(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  const shown = listening && completedHere;
  return (
    // Always present, so the notice that fills it is announced; empty, it
    // is out of the card's flow and takes no gap.
    <output className={shown ? "block" : "sr-only"}>
      {shown && (
        <span className="block rounded-md bg-success/8 px-3 py-2 text-sm text-success dark:bg-success/12">
          <strong className="font-semibold">{setupDone.result}</strong> {setupDone.before}{" "}
          <code>{setupDone.setting}</code> {setupDone.after}
        </span>
      )}
    </output>
  );
}
