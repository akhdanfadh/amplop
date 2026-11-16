import { useEffect, useId, useRef, useState, type ReactElement, type ReactNode } from "react";
import { Toolbar } from "react-aria-components";
import { cn } from "@/lib/cn";

// ReadingPane is what the reading pane shows (DESIGN.md "Reading pane"): a
// toolbar pinned to the top, which shows title once the title has scrolled
// away; then an article headed by title, with byline under it, e.g. the
// account, and marks, where the item is, e.g. a conversation's labels;
// then children. It takes focus when it opens, on its title, so a
// screen reader starts there. It scrolls itself, so give it a height, and
// a key per item, so the next starts afresh.
//
// The toolbar's slots hold what the page builds: back, the way back to
// the list at its start, where one pane shows at a time; actions, the
// item's actions at its end, named actionsLabel.
export function ReadingPane({
  title,
  byline,
  marks,
  back,
  actions,
  actionsLabel,
  children,
}: {
  title: string;
  byline: ReactNode;
  marks?: ReactNode;
  back?: ReactNode;
  actions?: ReactNode;
  actionsLabel?: string;
  children: ReactNode;
}): ReactElement {
  const id = useId();
  const scroller = useRef<HTMLDivElement>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const [titleHidden, setTitleHidden] = useState(false);

  useEffect(() => {
    // The view opens scrolled to its top, so focus moves the eye nowhere.
    heading.current?.focus({ preventScroll: true });
  }, []);

  useEffect(() => {
    const bar = toolbar.current;
    const pane = scroller.current;
    if (!bar || !pane) return undefined;
    // Whatever scrolls into view, by focus or by a link, stops below the
    // pinned toolbar (WCAG 2.4.11); once it no longer pins, nothing covers.
    const observer = new ResizeObserver(() => {
      const pinned = getComputedStyle(bar).position === "sticky";
      pane.style.scrollPaddingTop = pinned ? `${bar.offsetHeight}px` : "";
    });
    observer.observe(bar);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const target = heading.current;
    if (!target) return undefined;
    // The title counts as gone once it is under the pinned toolbar.
    const observer = new IntersectionObserver(([entry]) => setTitleHidden(!entry.isIntersecting), {
      root: scroller.current,
      rootMargin: `-${toolbar.current?.offsetHeight ?? 0}px 0px 0px 0px`,
    });
    observer.observe(target);
    return () => observer.disconnect();
  }, []);

  return (
    // scroll-pb: what scrolls into view stops above a toast (toast.tsx).
    <div
      ref={scroller}
      className="@container h-full scroll-pb-(--toast-space) overflow-y-auto bg-background"
    >
      <div
        ref={toolbar}
        data-raised={titleHidden || undefined}
        // Wraps only when large text leaves no room; DESIGN.md keeps five
        // buttons and More.
        // Pinned while its buttons fit one row: under 20em, as large text
        // makes a phone's pane (six 44 px buttons and the gutters), it
        // would take a third of the screen, so it scrolls away instead.
        // The query counts in em, so it follows the text size, not pixels.
        className="sticky top-0 z-10 flex min-h-12 flex-wrap items-center gap-1 border-b border-transparent bg-background px-4 data-raised:border-border @max-[20em]:static"
      >
        {back}
        <p
          // The title says it already, for anyone reading in order; and as
          // a copy, faded out until then, it takes no pointer or selection.
          aria-hidden="true"
          // A subject's direction from its first letter, as the list's.
          dir="auto"
          // Under a 512 px pane it may shrink, so the toolbar keeps one
          // row (ADR031, "Mail actions as built", checkpoint 7c); under
          // 28em, e.g. a phone, it would be a few letters between Back and
          // the actions, so it hides. In em, so it follows the text size.
          className={cn(
            "pointer-events-none min-w-0 flex-1 truncate px-2 text-sm font-semibold transition-opacity duration-150 select-none @max-[28em]:hidden @lg:min-w-24",
            titleHidden ? "opacity-100" : "opacity-0",
          )}
        >
          {title}
        </p>
        {actions && (
          // React Aria's toolbar: Tab reaches it once, arrow keys move
          // between its buttons.
          <Toolbar
            aria-label={actionsLabel}
            className="ml-auto flex flex-wrap items-center gap-0.5"
          >
            {actions}
          </Toolbar>
        )}
      </div>
      <article aria-labelledby={`${id}-title`} className="flex flex-col px-4 pb-6 @lg:px-6">
        <header className="flex flex-col gap-1 pt-2 pb-4">
          <h2
            ref={heading}
            id={`${id}-title`}
            tabIndex={-1}
            dir="auto"
            className="text-subject font-semibold wrap-break-word outline-none"
          >
            {title}
          </h2>
          <p className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">{byline}</p>
          {marks}
        </header>
        {children}
      </article>
    </div>
  );
}
