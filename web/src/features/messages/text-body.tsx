import type { ReactElement } from "react";
import Linkify from "linkify-react";
import { textLink } from "@/components/link-style";
import { linkOptions } from "./links";

// TextBody shows a plain-text body as text, keeping its line breaks and
// spaces, with its web and email addresses as links, in the direction its
// own text runs, e.g. right to left for Arabic. React escapes the text, so
// nothing in it reads as HTML.
export function TextBody({ text }: { text: string }): ReactElement {
  return (
    <Linkify
      as="div"
      dir="auto"
      options={{ ...linkOptions, className: textLink }}
      className="text-message wrap-break-word whitespace-pre-wrap"
    >
      {text}
    </Linkify>
  );
}
