import type { ServerResponse } from "node:http";
import type { Plugin } from "vite-plus";

// The browser tests' stand-in for inbok's message routes, on the test
// page's own origin as inbok's are on the app's. Bodies come with the
// headers the Go server's body route sends (backend/internal/httpapi,
// messagebody.go), so the frame meets the real sandbox and policy.
export const messagePath = "/test-messages/";

// The Go server's bodyPolicy with remote images blocked. Which images it
// lets in is the Go route's to test; the frame needs only to meet it.
const bodyPolicy =
  "sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox; default-src 'none'; " +
  "style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'; img-src 'self'";

// The cookie an inline image needs, as inbok's part route needs the session
// cookie; tests set it on the test page.
export const sessionCookie = "test-session=yes";

// A 1x1 PNG. Tests import this file in the browser, which has no Buffer,
// so it is decoded only when served.
const png =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

// A banner 1200 px wide, wider than 60% of any test frame fits.
const banner =
  '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="100"><rect width="1200" height="100"/></svg>';

// Every sanitized body starts with it (messagecontent's doctype), so a
// browser lays bodies out in standards mode.
const doctype = "<!DOCTYPE html>";

// The bodies by name, each served at messagePath + name.
const bodies: Record<string, string> = {
  // Mail that fits any width.
  fluid: "<p>Hello, this is a short message.</p>",
  // Mail laid out at a fixed width, as most newsletters are.
  wide600: '<table width="600" style="height:300px"><tr><td>Newsletter</td></tr></table>',
  wide1200: '<table width="1200" style="height:300px"><tr><td>Poster</td></tr></table>',
  // A body that tries to run a script and to fill the frame's height.
  script:
    '<html style="height:100%"><body style="height:100%;padding:20px">' +
    "<script>parent.scriptRan = true</script><p>Hello</p></body></html>",
  // An inline image, which loads only with the cookie.
  image: `<img alt="Logo" src="${messagePath}image.png" width="40" height="40">`,
  // Mail whose own CSS hides the root's overflow, wider than 60% fits.
  hiddenOverflow:
    "<style>html { overflow-x: hidden }</style>" +
    '<table width="1200" style="height:300px"><tr><td>Poster</td></tr></table>',
  // Mail that fixes its root's width, so the root keeps its size as the
  // frame changes.
  fixedRoot:
    "<style>html { width: 600px } body { margin: 0 }</style>" +
    '<table width="600" style="height:300px"><tr><td>Newsletter</td></tr></table>',
  // An image whose server never answers, as a slow remote one may not.
  slowImage:
    `<img alt="Banner" src="${messagePath}never.png" width="40" height="40">` +
    "<p>Hello from far away.</p>",
  // A banner whose width is known only once it arrives, below a fixed
  // height, so its arrival widens the body without changing its height;
  // beside an image that never arrives, so the frame's load event never
  // comes.
  lateBanner:
    `<img alt="Banner" src="${messagePath}late.svg" style="display:block;height:100px">` +
    `<img alt="Logo" src="${messagePath}never.png" width="40" height="40">`,
  // A body that grows after it is read.
  details:
    '<details><summary>More</summary><div style="height:500px">Hidden</div></details>' +
    "<p>Short</p>",
};

// messageServer serves the bodies at messagePath, plus:
// - image.png: the image, or 404 without sessionCookie;
// - never.png: an image whose answer never comes;
// - late.svg: the banner, held back until release-late is asked for;
// - error: a JSON problem refused a frame, as every API error is;
// - stall: a body that starts and never ends.
// Anything after a body's name and a "?" is ignored, so tests can give one
// body two URLs.
export function messageServer(): Plugin {
  // The late.svg answers held back.
  const late: ServerResponse[] = [];
  return {
    name: "inbok-test-messages",
    configureServer(server) {
      server.middlewares.use(messagePath, (req, res) => {
        const url = new URL(req.url ?? "", "http://test");
        const name = url.pathname.replace(/^\//, "");
        if (name === "image.png") {
          const cookie = req.headers.cookie ?? "";
          res.statusCode = cookie.split("; ").includes(sessionCookie) ? 200 : 404;
          res.setHeader("Content-Type", "image/png");
          res.end(res.statusCode === 200 ? Buffer.from(png, "base64") : undefined);
          return;
        }
        if (name === "never.png") return;
        if (name === "late.svg") {
          late.push(res);
          return;
        }
        if (name === "release-late") {
          for (const held of late.splice(0)) {
            held.setHeader("Content-Type", "image/svg+xml");
            held.end(banner);
          }
          res.end();
          return;
        }
        if (name === "error") {
          res.statusCode = 404;
          res.setHeader("Content-Type", "application/problem+json");
          res.setHeader("X-Frame-Options", "DENY");
          res.end('{"status":404,"title":"Not Found"}');
          return;
        }
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.setHeader("Content-Security-Policy", bodyPolicy);
        res.setHeader("X-Frame-Options", "SAMEORIGIN");
        res.setHeader("Cache-Control", "no-store");
        if (name === "stall") {
          // Enough to start the document; the rest never comes.
          res.write(`${doctype}<html><body><p>The start of a message`);
          return;
        }
        const body = bodies[name];
        if (body === undefined) {
          res.statusCode = 404;
          res.end();
          return;
        }
        res.end(doctype + body);
      });
    },
  };
}
