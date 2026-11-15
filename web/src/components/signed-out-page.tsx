import { useLayoutEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { stepEnter } from "@/components/step-change";
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";

// SignedOutPage is a signed-out page's content (DESIGN.md "Settings and
// signed-out pages"): a card with the page's title, address on a line of
// its own when the page is about one, e.g. "Check your email", a one-line
// lead, and children, the form or message, then after, e.g. a link out of
// the flow, under the card. The title takes focus when the page opens and
// again when step changes, e.g. from the form to its outcome; describedBy
// names an element it reads with it, e.g. a notice above the form.
export function SignedOutPage({
  title,
  address,
  lead,
  step,
  describedBy,
  after,
  children,
}: {
  title: string;
  address?: string;
  lead?: string;
  step?: string;
  describedBy?: string;
  after?: ReactNode;
  children?: ReactNode;
}): ReactElement {
  const titleRef = useRef<HTMLHeadingElement>(null);
  // Whether the step, and the title, have changed since the page opened:
  // new content fades in and rises 4 px (DESIGN.md "Motion"); the first is
  // there at once. Set during render, React's way to keep what an earlier
  // render saw (react.dev, "Storing information from previous renders").
  const [first] = useState({ step, title });
  const [stepped, setStepped] = useState(false);
  const [retitled, setRetitled] = useState(false);
  if (!stepped && step !== first.step) setStepped(true);
  if (!retitled && title !== first.title) setRetitled(true);
  // The header is keyed by title and the content by step, so each enters
  // afresh when it changes. The header's entrance waits for a new title:
  // adding the class to a header that stays would start it anyway, e.g.
  // when a check finds the form under the same title.

  // A new page, or a new step on it, replaces what had focus, so a screen
  // reader starts from its title. A layout effect runs before the content's
  // own effects, so content may move focus on afterwards, e.g. to the field
  // of a form shown again.
  useLayoutEffect(() => {
    titleRef.current?.focus();
  }, [step]);

  return (
    <main className="flex flex-col gap-6 wrap-anywhere">
      <Card className="gap-6 py-8 [--card-spacing:--spacing(6)]">
        <CardHeader
          // Keyed by title: a step that keeps its title, e.g. a check
          // that finds the form, keeps its header still.
          key={title}
          className={cn("justify-items-center text-center", retitled && stepEnter)}
        >
          <h1
            ref={titleRef}
            tabIndex={-1}
            className="text-2xl/8 font-semibold text-balance outline-none"
            aria-describedby={describedBy}
          >
            {title}
          </h1>
          {address && <AddressLine>{address}</AddressLine>}
          {lead && <CardDescription>{lead}</CardDescription>}
        </CardHeader>
        {children && (
          <CardContent
            key={`${step}-content`}
            className={cn("flex flex-col gap-6", stepped && stepEnter)}
          >
            {children}
          </CardContent>
        )}
      </Card>
      {after}
    </main>
  );
}

// AddressLine shows an email address as one value to check, on a line of
// its own in the text colour, not broken into a sentence's words: a long
// address wraps at its own hyphens and reads like hyphenated words
// (DESIGN.md "Outcome screens").
export function AddressLine({ children }: { children: string }): ReactElement {
  return (
    // line-break: anywhere fills each line to its edge, so a line ends on
    // a hyphen only by chance.
    <span className="block font-medium text-foreground [line-break:anywhere]">{children}</span>
  );
}
