import type { Opts } from "linkifyjs";

// A link without a scheme, e.g. example.com, opens over https.
const defaultProtocol = "https";
// The schemes a plain-text body's links may use, the ones the server's
// sanitizer keeps on links in HTML but tel, which linkifyjs never finds.
// linkifyjs also finds file:, ftp:, and ftps:, which a mail reader has no
// reason to open.
const linkable = /^(?:https?:\/\/|mailto:)/i;
// Direction overrides and isolates (Unicode Bidirectional Algorithm, 2.1
// and 2.2) in a link would show its text in another order than the
// browser follows, e.g. ".../\u202Egnp.exe" reads as ending in ".png".
const bidiControls = /[\u202A-\u202E\u2066-\u2069]/;

// linkOptions makes linkifyjs turn the web and email addresses in a
// plain-text body into links that open in a new tab, as links in an HTML
// body do, and leaves any other scheme, or a link with a direction
// control in it, as text.
export const linkOptions: Opts = {
  defaultProtocol,
  target: "_blank",
  rel: "noopener noreferrer",
  validate: (value, _type, token) =>
    linkable.test(token.toHref(defaultProtocol)) && !bidiControls.test(value),
};
