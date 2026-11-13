import type { ReactElement } from "react";
import Linkify from "linkify-react";
import { textLink } from "@/components/link-style";
import { linkOptions } from "./links";

// TextBody shows a plain-text body as text, keeping its line breaks and
// spaces, with its web and email addresses as links. React escapes the
// text, so nothing in it reads as HTML.
export function TextBody({ text }: { text: string }): ReactElement {
  return (
    <Linkify
      as="div"
      options={{ ...linkOptions, className: textLink }}
      className="wrap-break-word whitespace-pre-wrap"
    >
      {text}
    </Linkify>
  );
}
