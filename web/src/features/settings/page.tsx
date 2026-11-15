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
import { cn } from "@/lib/cn";

// SettingsPage is one settings page (DESIGN.md "Settings and signed-out
// pages"): back, e.g. a link up to the page it belongs to, the title, aside
// it, e.g. a status badge, a one-sentence lead, notice, a result to report,
// e.g. "Two-factor authentication is on.", then the sections, divided by
// hairlines, capped at 720 px. The title takes focus when the page opens,
// so a screen reader starts there.
export function SettingsPage({
  title,
  aside,
  lead,
  back,
  notice,
  children,
}: {
  title: string;
  aside?: ReactNode;
  lead: string;
  back?: ReactNode;
  notice?: string;
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
    <main className="@container mx-auto w-full max-w-180 px-4 pt-6 pb-12 wrap-anywhere sm:px-6 sm:pt-8">
      {back}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h1 ref={titleRef} tabIndex={-1} className="text-2xl font-semibold outline-none">
          {title}
        </h1>
        {aside}
      </div>
      <p className="mt-1 text-muted-foreground">{lead}</p>
      {/* Always present, so a screen reader announces the notice that fills
          it; empty, it takes no space. */}
      <output className="block">
        {listening && notice && (
          <span className="mt-4 block w-fit rounded-md bg-success/8 px-3 py-2 text-sm text-success dark:bg-success/12">
            {notice}
          </span>
        )}
      </output>
      <div className="mt-6 divide-y border-t">{children}</div>
    </main>
  );
}

// SettingsSection is one section of a SettingsPage: the heading, aside, e.g.
// a status badge, and about, one sentence on what the section is for, on the
// left, and the controls on the right. layout "stacked" puts the heading
// over the controls so they get the whole width, e.g. for a step with a QR
// code; below 512 px of page every section stacks. headingRef reaches the heading,
// e.g. to give it focus when the control that had it goes away.
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
  layout?: "columns" | "stacked";
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
      <div className="flex min-w-0 flex-col items-start gap-4">{children}</div>
    </section>
  );
}
