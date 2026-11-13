// The HTML body's frame: what it may do, and how it shows its body.

// sandbox is the frame's sandbox, which the body's own
// Content-Security-Policy repeats. Without allow-scripts nothing in the
// body runs; allow-same-origin lets its inline images load with the
// session cookie and lets this page read its height; links open in a new
// tab, outside the sandbox.
export const sandbox = "allow-same-origin allow-popups allow-popups-to-escape-sandbox";

// What a frame is doing with its body.
export type FrameState = "loading" | "shown" | "failed";

const htmlType = "text/html";
// The frame's first document, before the body's replaces it.
const initialURL = "about:blank";
// How often to look whether the body's document has been read, inbok's
// choice: within a tenth of a second a change reads as instant (Nielsen,
// "Response Times: The 3 Important Limits"), and a look reads one
// property. No event tells this page that the frame's document is read;
// the load event waits for every image, so a slow remote image would hold
// back the whole message.
const pollMs = 100;
// How long a body may take to be read before the view gives up, e.g. on a
// stream cut off midway, which leaves the document unfinished. inbok's
// choice: the body comes from inbok's own store, normally in well under a
// second, so this leaves room for a slow network.
const timeoutMs = 15_000;
// The smallest scale wide mail is shown at, after which it scrolls
// sideways (ADR031, "Reading pane"): smaller, its text gets hard to read.
const minScale = 0.6;

// watch shows the body in frame once its document has been read, without
// waiting for its images, and from then on keeps the frame as tall as the
// body and the body scaled to the frame's width. It reports each state to
// report and returns a function that stops watching.
export function watch(frame: HTMLIFrameElement, report: (state: FrameState) => void): () => void {
  const deadline = Date.now() + timeoutMs;
  // The body's document, once shown.
  let shown: Document | undefined;
  let observer: ResizeObserver | undefined;

  function refit(): void {
    if (shown) fit(frame, shown);
  }

  function stop(): void {
    clearInterval(timer);
    frame.removeEventListener("load", handleLoad);
    observer?.disconnect();
    shown?.removeEventListener("load", refit, true);
    shown?.removeEventListener("error", refit, true);
  }

  function fail(): void {
    stop();
    report("failed");
  }

  function check(): void {
    const doc = frame.contentDocument;
    // 1. Fail on an answer that is no body, before waiting on it: an error
    // answer from the API is refused a frame, which leaves a document this
    // page cannot read, or is JSON, which shows as text.
    if (!doc || (doc.URL !== initialURL && doc.contentType !== htmlType)) return fail();

    // 2. Wait while the frame holds its first, empty document or is still
    // reading the body, until the deadline.
    if (doc.URL === initialURL || doc.readyState === "loading") {
      if (Date.now() > deadline) fail();
      return;
    }

    // 3. Stop looking, and let the body's root and body be as tall as their
    // content before anything measures them.
    clearInterval(timer);
    shown = doc;
    autoHeight(doc);

    // 4. Refit from now on: a body changes size after it is read, e.g. as
    // its images arrive or a details element opens, and a new frame width
    // rewraps it. The root's size follows both, unless the mail fixes its
    // width, so the frame's is watched too.
    observer = new ResizeObserver(refit);
    observer.observe(doc.documentElement);
    observer.observe(frame);
    // An image can widen the body and leave the root's box as it was, e.g.
    // one whose height the mail fixes, so each image's arrival refits too.
    // Image load and error events do not bubble; they reach the document
    // only while capturing.
    doc.addEventListener("load", refit, true);
    doc.addEventListener("error", refit, true);

    // 5. Fit it now and show it.
    fit(frame, doc);
    report("shown");
  }

  function handleLoad(): void {
    if (!shown) return check();
    // Another page in the frame, e.g. from a link without a target, which
    // the server never writes.
    if (frame.contentDocument !== shown) fail();
  }

  const timer = setInterval(check, pollMs);
  frame.addEventListener("load", handleLoad);
  return stop;
}

// autoHeight lets doc's root and body be as tall as their content. A body
// set to height:100% of the frame, plus its padding, would otherwise
// always be taller than the frame fitted to it.
function autoHeight(doc: Document): void {
  for (const element of [doc.documentElement, doc.body]) {
    element?.style.setProperty("height", "auto", "important");
    element?.style.setProperty("min-height", "0", "important");
  }
}

// fit scales doc, the body frame shows, to the frame's width, down to
// minScale, and sets the frame's height to the body's, so the page
// scrolls the message rather than the frame.
//
// DESIGN: CSS zoom rather than a transform, so the scaled body takes the
// space it shows in, and its root's sizes come back scaled. Each step
// measures and sets in one go, without yielding, so the ResizeObserver
// that calls fit sees only the result and is not called again by it.
function fit(frame: HTMLIFrameElement, doc: Document): void {
  const root = doc.documentElement;
  const view = doc.defaultView;
  if (!view) return;

  // 1. Scale the body to the frame, measured at full size: scaled down,
  // mail that fills the frame's width reads as fitting at any scale, so it
  // would stay small after the frame widens. Measured without scroll
  // bars: a frame still shorter than its body shows a vertical one, which
  // the fitted frame does not.
  root.style.setProperty("overflow", "hidden", "important");
  root.style.setProperty("zoom", "1", "important");
  const scale = Math.min(1, Math.max(minScale, root.clientWidth / root.scrollWidth));
  root.style.setProperty("zoom", String(scale), "important");
  root.style.removeProperty("overflow");
  // Mail still wider than the frame scrolls sideways, even where its own
  // CSS hides the root's overflow, which would cut its right side off.
  root.style.setProperty("overflow-x", "auto", "important");

  // 2. Collapse the frame. A document is never shorter than its frame, so
  // measuring at the old height could only grow it, never shrink it.
  frame.style.height = "0";
  frame.style.height = `${root.scrollHeight}px`;

  // 3. Make room for a horizontal scroll bar, which mail wider than the
  // frame at minScale shows inside the frame and which its own height
  // leaves out.
  const scrollBar = view.innerHeight - root.clientHeight;
  if (scrollBar > 0) frame.style.height = `${root.scrollHeight + scrollBar}px`;
}
