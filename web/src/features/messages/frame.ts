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

// watch shows the body in frame once its document has been read, without
// waiting for its images, and keeps the frame as tall as the body from
// then on. It reports each state to report and returns a function that
// stops watching.
export function watch(frame: HTMLIFrameElement, report: (state: FrameState) => void): () => void {
  const deadline = Date.now() + timeoutMs;
  // The body's document, once shown.
  let shown: Document | undefined;
  let observer: ResizeObserver | undefined;

  function stop(): void {
    clearInterval(timer);
    frame.removeEventListener("load", handleLoad);
    observer?.disconnect();
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

    // 4. Refit from now on: a body changes height after it is read, e.g. as
    // its images arrive or a details element opens, and the frame's width
    // rewraps it.
    observer = new ResizeObserver(() => fit(frame, doc));
    observer.observe(doc.documentElement);

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

// fit sets frame's height to the height of doc, the body it shows, so the
// page scrolls the message rather than the frame.
function fit(frame: HTMLIFrameElement, doc: Document): void {
  const root = doc.documentElement;

  // 1. Collapse the frame. A document is never shorter than its frame, so
  // measuring at the old height could only grow it, never shrink it.
  frame.style.height = "0";
  frame.style.height = `${root.scrollHeight}px`;

  // 2. Make room for a horizontal scroll bar, which a wide body shows
  // inside the frame and which its own height leaves out.
  const scrollBar = (doc.defaultView?.innerHeight ?? 0) - root.clientHeight;
  if (scrollBar > 0) frame.style.height = `${root.scrollHeight + scrollBar}px`;
}
