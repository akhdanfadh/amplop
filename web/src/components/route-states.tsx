import {
  createContext,
  use,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  Link,
  SearchParamError,
  useRouter,
  useRouterState,
  type ErrorComponentProps,
} from "@tanstack/react-router";
import { cn } from "@/lib/cn";
import { sentence, userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { ReadError } from "@/components/read-error";
import { Button, buttonVariants } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { textLink } from "@/components/link-style";
import { isSessionEnded } from "@/features/auth/guard";
import { SignInAgain } from "@/features/auth/session-ended-banner";

// StateRegion says where a route state shows: "page" in place of a whole
// page, or "pane" in a pane of a page that holds its own main and h1, e.g.
// the mail reading pane beside the list.
export const StateRegion = createContext<"page" | "pane">("page");

// StatePage is the body of a state that replaces a page, or a pane in one:
// title as the tab's title and as a heading that takes focus, then
// children.
function StatePage({ title, children }: { title: string; children: ReactNode }): ReactElement {
  const region = use(StateRegion);
  const headingId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // A screen reader starts reading at the heading.
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  const className =
    "mx-auto flex w-full max-w-lg flex-col gap-4 px-4 py-8 wrap-anywhere sm:px-6 sm:py-12";
  const heading = {
    ref: headingRef,
    id: headingId,
    tabIndex: -1,
    className: "text-2xl font-semibold",
  };
  // A page has one main landmark and one h1, which a pane's page holds.
  if (region === "pane") {
    return (
      <section className={className} aria-labelledby={headingId}>
        <title>{`${title} - inbok`}</title>
        <h2 {...heading}>{title}</h2>
        {children}
      </section>
    );
  }
  return (
    <main className={className}>
      {/* React puts it in the head, and takes it out when the state ends. */}
      <title>{`${title} - inbok`}</title>
      <h1 {...heading}>{title}</h1>
      {children}
    </main>
  );
}

// pendingAfterMs is how long a page's checks run before its pending view
// shows (DESIGN.md "States").
export const pendingAfterMs = 300;

// RoutePending is shown while a page waits for what it needs before it
// can draw, e.g. the session check. The router shows it after 300 ms, or
// at once after Try again. moment says which load it is part of: the
// app's "first-load", the setup check's included, or a "later" one; the
// router's own says by default.
export function RoutePending({ moment }: { moment?: "first-load" | "later" }): ReactElement {
  const routerFirstLoad = useRouterState({
    select: (state) => state.resolvedLocation === undefined,
  });
  // TanStack shows the first load's view at once (router.tsx), so CSS
  // hides it for 300 ms instead.
  const firstLoad = moment === undefined ? routerFirstLoad : moment === "first-load";
  // The router's first-load view appears only once the setup check's ends,
  // so its 300 ms count from the page's start, as the setup check's do:
  // one loading period, which a fresh 300 ms would blank in the middle.
  // The setup check's view keeps CSS's own count, which starts as the
  // prerendered page is read.
  const [hiddenMs] = useState(() =>
    moment === undefined && routerFirstLoad
      ? Math.max(0, pendingAfterMs - performance.now())
      : undefined,
  );
  const statusRef = useRef<HTMLOutputElement>(null);
  // After Try again the page that held focus is hidden (React keeps it,
  // with display: none, until the new one is ready) or gone, and focus
  // would fall to the document; the status takes it, so a screen reader
  // hears it and Tab goes on from here. Focus on anything still shown
  // stays put, and the first load has nothing to keep.
  useEffect(() => {
    if (firstLoad) return;
    const active = document.activeElement;
    if (!active || active === document.body || !active.checkVisibility()) {
      statusRef.current?.focus();
    }
  }, [firstLoad]);

  return (
    <output
      ref={statusRef}
      tabIndex={-1}
      className={cn(
        "flex flex-1 items-center justify-center gap-2 p-8 text-muted-foreground",
        firstLoad && "pending-delay",
      )}
      style={hiddenMs === undefined ? undefined : { animationDelay: `${hiddenMs}ms` }}
    >
      <Spinner />
      Loading...
    </output>
  );
}

// FailedRead replaces a page whose read failed: what failed and that
// nothing saved is lost, error's request reference, and Try again, which
// runs onRetry. The caller swaps it for its pending view at once, so there
// is no second press to block.
export function FailedRead({
  error,
  onRetry,
}: {
  error: ApiError;
  onRetry: () => void;
}): ReactElement {
  return (
    <StatePage title="Couldn't load this page">
      <ReadError
        message={`${sentence(userMessage(error))} Nothing you saved is lost.`}
        error={error}
        retrying={false}
        onRetry={onRetry}
        placement="whole-page"
      />
    </StatePage>
  );
}

// RouteError replaces a page that failed: a read it needs (an ApiError),
// with Try again, which runs the page's checks again, or Sign in when the
// read found the session ended (a 401); an address whose search
// parameters its route refuses, as Page not found; or its own code, with
// Reload. A route that refuses a name in its path throws notFound()
// from its beforeLoad or loader, which shows Page not found; thrown while
// the page draws, it is a crash.
export function RouteError({ error }: ErrorComponentProps): ReactElement {
  const router = useRouter();

  // The address leads to no page, e.g. an old link's ?account=abc; Reload
  // would load the same address and fail again.
  if (error instanceof SearchParamError) {
    return <NotFound />;
  }
  if (isSessionEnded(error)) {
    return <SignedOut />;
  }
  if (error instanceof ApiError) {
    return <FailedRead error={error} onRetry={() => void router.invalidate()} />;
  }
  return <CrashScreen />;
}

// CrashScreen replaces a page, or the whole app when the code around the
// pages failed: reloading starts it afresh, and the start page is the way
// out when the same page fails every time.
export function CrashScreen(): ReactElement {
  return (
    <StatePage title="This page stopped working">
      <p>inbok ran into a problem it didn&apos;t expect. Nothing you saved is lost.</p>
      <Button
        type="button"
        variant="outline"
        className="w-fit"
        onPress={() => window.location.reload()}
      >
        Reload
      </Button>
      <p>
        If it happens again, {/* A page load, not the router, for a fresh start after a crash. */}
        <a href="/" className={textLink}>
          open inbok&apos;s start page
        </a>
        .
      </p>
    </StatePage>
  );
}

// SignedOut replaces a page that could not load because the session ended
// while another part of the app, e.g. the compose window, kept work to
// copy: Try again would fail the same way, so it offers sign-in, which
// returns here.
function SignedOut(): ReactElement {
  return (
    <StatePage title="You're signed out">
      <p>This page can&apos;t load until you sign in again.</p>
      <SignInAgain className={cn(buttonVariants({ variant: "outline" }), "w-fit")}>
        Sign in
      </SignInAgain>
    </StatePage>
  );
}

// NotFound is shown for an address inbok has no page for, e.g. a mistyped
// one or a link to something removed.
export function NotFound(): ReactElement {
  return (
    <StatePage title="Page not found">
      <p>
        There&apos;s no page at this address. Check it for typing mistakes, or{" "}
        <Link to="/" className={textLink}>
          open inbok&apos;s start page
        </Link>
        .
      </p>
    </StatePage>
  );
}
