import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
  type Ref,
} from "react";
import { CircleAlertIcon, CircleCheckIcon, InfoIcon } from "lucide-react";
import { cn } from "@/lib/cn";

// NoticeTone is what a page's notice reports: a change that worked, news
// that changed nothing, or a problem the member can fix.
export type NoticeTone = "success" | "neutral" | "problem";

// Each tone's look: its icon, so the tone never rests on colour alone, and
// its tint.
const noticeLooks = {
  success: { icon: CircleCheckIcon, box: "bg-success/8 text-success dark:bg-success/12" },
  neutral: { icon: InfoIcon, box: "bg-muted text-foreground" },
  problem: {
    icon: CircleAlertIcon,
    box: "bg-destructive/8 text-destructive dark:bg-destructive/12",
  },
} as const;

// SettingsPage is one settings page (DESIGN.md "Settings and signed-out
// pages"): back, e.g. a link up to the page it belongs to, the title, aside
// it, e.g. a status badge, a one-sentence lead, notice, a result to report,
// e.g. "Two-factor authentication is on.", in noticeTone's look, success by
// default, where a problem is an alert, then the sections, divided by
// hairlines, capped at 720 px. height "fill" keeps the page to the window
// and gives its last section, laid out "fill", the height left, e.g. for a
// table that scrolls inside it. The title takes focus when the page opens,
// so a screen reader starts there.
export function SettingsPage({
  title,
  aside,
  lead,
  back,
  notice,
  noticeTone = "success",
  height = "content",
  children,
}: {
  title: string;
  aside?: ReactNode;
  lead: string;
  back?: ReactNode;
  notice?: string;
  noticeTone?: NoticeTone;
  height?: "content" | "fill";
  children: ReactNode;
}): ReactElement {
  const titleRef = useRef<HTMLHeadingElement>(null);
  // Whether the notice region exists yet. A screen reader announces a live
  // region's change, not what it already holds when it appears (MDN, "ARIA
  // live regions"), so a notice that comes with the page fills it a frame
  // after the region is drawn.
  const [listening, setListening] = useState(false);

  // A screen reader starts from the title. A layout effect runs before the
  // content's own effects, so content may move focus on afterwards, e.g. to
  // the field of a step shown with the page.
  useLayoutEffect(() => {
    titleRef.current?.focus();
  }, []);

  useEffect(() => {
    const frame = requestAnimationFrame(() => setListening(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  return (
    // A container, so a section lays itself out by the page's width, not
    // the window's: the settings sidebar takes part of a wide window.
    <main
      className={cn(
        "@container mx-auto w-full max-w-180 px-4 pt-6 pb-12 wrap-anywhere sm:px-6 sm:pt-8",
        // Shrinks to the frame's height; below its last section's floor the
        // frame scrolls as for any page.
        height === "fill" && "flex min-h-0 flex-1 flex-col pb-6",
      )}
    >
      {back}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h1 ref={titleRef} tabIndex={-1} className="text-2xl font-semibold outline-none">
          {title}
        </h1>
        {aside}
      </div>
      <p className="mt-1 text-sm text-muted-foreground">{lead}</p>
      {/* Always present, so a screen reader announces the notice that fills
          it; empty, it takes no space. */}
      <output className="block">
        {listening && notice && noticeTone !== "problem" && (
          <NoticeBox tone={noticeTone}>{notice}</NoticeBox>
        )}
      </output>
      {/* A problem interrupts: a screen reader reads an alert as it
          appears, not only when the member is idle. */}
      {listening && notice && noticeTone === "problem" && (
        <div role="alert">
          <NoticeBox tone="problem">{notice}</NoticeBox>
        </div>
      )}
      <div
        className={cn(
          "mt-6 divide-y border-t",
          height === "fill" && "flex min-h-0 flex-1 flex-col",
        )}
      >
        {children}
      </div>
    </main>
  );
}

// NoticeBox is a page's notice, children, in tone's look.
function NoticeBox({ tone, children }: { tone: NoticeTone; children: string }): ReactElement {
  const { icon: Icon, box } = noticeLooks[tone];
  return (
    <span className={cn("mt-4 flex w-fit items-start gap-2 rounded-md px-3 py-2 text-sm", box)}>
      {/* On the first line's centre, however many lines the text takes. */}
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <span className="min-w-0">{children}</span>
    </span>
  );
}

// SettingsSection is one section of a SettingsPage: the heading, aside, e.g.
// a status badge, and about, one sentence on what the section is for, on the
// left, and the controls on the right. layout "stacked" puts the heading
// over the controls so they get the whole width, e.g. for a step with a QR
// code; below 512 px of page every section stacks. layout "fill" stacks too
// and takes the height a SettingsPage of height "fill" leaves, for controls
// that scroll inside it. headingRef reaches the heading, e.g. to give it
// focus when the control that had it goes away.
export function SettingsSection({
  title,
  about,
  aside,
  layout = "columns",
  headingRef,
  children,
}: {
  title: string;
  about: string;
  aside?: ReactNode;
  layout?: "columns" | "stacked" | "fill";
  headingRef?: Ref<HTMLHeadingElement>;
  children: ReactNode;
}): ReactElement {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      className={cn(
        "grid gap-4 py-6",
        layout === "columns" && "@lg:grid-cols-[minmax(0,13rem)_minmax(0,1fr)] @lg:gap-8",
        layout === "fill" && "flex min-h-0 flex-1 flex-col pb-0",
      )}
    >
      <div className="flex flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 id={id} ref={headingRef} tabIndex={-1} className="font-semibold outline-none">
            {title}
          </h2>
          {aside}
        </div>
        <p className="text-sm text-muted-foreground">{about}</p>
      </div>
      <div
        className={cn(
          "flex min-w-0 flex-col items-start gap-4 text-sm",
          layout === "fill" && "min-h-0 flex-1 items-stretch",
        )}
      >
        {children}
      </div>
    </section>
  );
}
