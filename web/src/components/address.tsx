import type { ReactElement } from "react";

// Address shows email, wrapping at its seams: first before the "@", so the
// local part and the domain each stay whole while they fit, then before a
// dot. A part too long for its line still breaks anywhere, e.g. a
// 64-character local part (RFC 5321), rather than spilling under the next
// column. Screen readers get the address whole.
export function Address({ email }: { email: string }): ReactElement {
  const at = email.lastIndexOf("@");
  const split = at < 0 ? email.length : at;
  // A whole half moves to the next line before it breaks inside.
  const half = "inline-block max-w-full";
  return (
    <span translate="no">
      {/* Chrome joins the parts below with spaces when it names a dialog or
          a cell, so the parts are for the eye and this is for the ear. */}
      <span className="sr-only">{email}</span>
      <span aria-hidden="true" className="wrap-anywhere">
        <span className={half}>{dotted(email.slice(0, split))}</span>
        <wbr />
        <span className={half}>{dotted(email.slice(split))}</span>
      </span>
    </span>
  );
}

// dotted returns text with a break opportunity before each dot.
function dotted(text: string): ReactElement[] {
  const parts: ReactElement[] = [];
  let offset = 0;
  for (const part of text.split(/(?=\.)/)) {
    if (offset > 0) parts.push(<wbr key={`break-${offset}`} />);
    parts.push(<span key={offset}>{part}</span>);
    offset += part.length;
  }
  return parts;
}
