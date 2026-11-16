import { useEffect, useState, type ReactElement, type ReactNode } from "react";
import { Link, useLocation } from "@tanstack/react-router";
import { LogOutIcon } from "lucide-react";
import { Banner } from "@/components/banner";
import { textLink } from "@/components/link-style";
import { useCompose } from "@/features/outgoing-messages/compose-store";
import { useKeptSignedOut } from "./session-end";

// The banner's landmark name.
export const signedOutLabel = "Signed out";

// SignInAgain links to sign-in from a page whose session ended, with
// children as its words: sign-in says the session ended and returns to
// this page afterwards. A leave warning on the page asks first.
export function SignInAgain({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}): ReactElement {
  const href = useLocation({ select: (location) => location.href });
  return (
    <Link
      to="/sign-in"
      search={{ redirect: href }}
      state={{ sessionEnded: true }}
      replace
      className={className}
    >
      {children}
    </Link>
  );
}

// SessionEndedBanner says, while the session has ended on a page that
// keeps work to copy (watchSessionEnd), that nothing here can be saved, with
// a link to sign in again, and nothing otherwise. place says where it
// stands: "frame" above the page, where the frame's other banners are, or
// "window" in the compose window, which shows it while it covers the page
// and the frame's is hidden under it; exactly one shows at a time.
export function SessionEndedBanner({ place }: { place: "frame" | "window" }): ReactElement {
  const ended = useKeptSignedOut();
  const covering = useCompose().state.covering;
  const shown = ended && covering === (place === "window");
  // Whether the live region exists yet: a screen reader announces a live
  // region's change, not what it already holds when it appears (MDN, "ARIA
  // live regions"), and the window may mount already signed out.
  const [listening, setListening] = useState(false);

  useEffect(() => {
    const frame = requestAnimationFrame(() => setListening(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  return (
    <output className="contents">
      {listening && shown && (
        // A landmark while it shows, so a screen reader that missed the
        // announcement finds it again (WAI-ARIA APG, "Landmark Regions").
        <section aria-label={signedOutLabel} className="shrink-0">
          <Banner tone="warning" icon={LogOutIcon}>
            You&apos;re signed out, so changes here can&apos;t be saved. Copy what you need, then{" "}
            <SignInAgain className={textLink}>sign in</SignInAgain>.
          </Banner>
        </section>
      )}
    </output>
  );
}
