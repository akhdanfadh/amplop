import { useEffect, useId, useRef, type ReactElement, type ReactNode, type Ref } from "react";

// SettingsPage is one settings page (DESIGN.md "Settings and signed-out
// pages"): back, e.g. a link up to the page it belongs to, the title, a
// one-sentence lead, then the sections, divided by hairlines, capped at
// 720 px. The title takes focus when the page opens, so a screen reader
// starts there.
export function SettingsPage({
  title,
  lead,
  back,
  children,
}: {
  title: string;
  lead: string;
  back?: ReactNode;
  children: ReactNode;
}): ReactElement {
  const titleRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    titleRef.current?.focus();
  }, []);

  return (
    // A container, so a section lays itself out by the page's width, not
    // the window's: the settings sidebar takes part of a wide window.
    <main className="@container mx-auto w-full max-w-180 px-4 pt-6 pb-12 wrap-anywhere sm:px-6 sm:pt-8">
      {back}
      <h1 ref={titleRef} tabIndex={-1} className="text-2xl font-semibold outline-none">
        {title}
      </h1>
      <p className="mt-1 text-muted-foreground">{lead}</p>
      <div className="mt-6 divide-y border-t">{children}</div>
    </main>
  );
}

// SettingsSection is one section of a SettingsPage: the heading, aside, e.g.
// a status badge, and about, one sentence on what the section is for, on the
// left, and the controls on the right; below 512 px of page they stack.
// headingRef reaches the heading, e.g. to give it focus when the control
// that had it goes away.
export function SettingsSection({
  title,
  about,
  aside,
  headingRef,
  children,
}: {
  title: string;
  about: string;
  aside?: ReactNode;
  headingRef?: Ref<HTMLHeadingElement>;
  children: ReactNode;
}): ReactElement {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      className="grid gap-4 py-6 @lg:grid-cols-[minmax(0,13rem)_minmax(0,1fr)] @lg:gap-8"
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
