import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { watch, type FrameState } from "./frame";

const bodyURL = "http://inbok.test/api/v0/body";

// element stands in for a document's root or body; style records each
// property set on it.
function element(scrollHeight: number, clientHeight: number) {
  const style: Record<string, string> = {};
  return {
    scrollHeight,
    clientHeight,
    style: {
      setProperty: (name: string, value: string, priority?: string) => {
        style[name] = priority ? `${value} !${priority}` : value;
      },
    },
    styles: style,
  };
}

// page stands in for a document a frame shows.
function page({
  url = bodyURL,
  contentType = "text/html",
  readyState = "interactive",
  scroll = 900,
  client = 900,
  inner = 900,
}: {
  url?: string;
  contentType?: string;
  readyState?: DocumentReadyState;
  scroll?: number;
  client?: number;
  inner?: number;
} = {}) {
  return {
    URL: url,
    contentType,
    readyState,
    documentElement: element(scroll, client),
    body: element(scroll, client),
    defaultView: { innerHeight: inner },
  };
}

// frame stands in for an iframe; heights records each height set on it.
function frame(doc: ReturnType<typeof page> | null) {
  const listeners = new Set<() => void>();
  const heights: string[] = [];
  return {
    contentDocument: doc,
    style: {
      get height() {
        return heights.at(-1) ?? "";
      },
      set height(value: string) {
        heights.push(value);
      },
    },
    addEventListener: (_type: "load", listener: () => void) => listeners.add(listener),
    removeEventListener: (_type: "load", listener: () => void) => listeners.delete(listener),
    load: () => {
      for (const listener of listeners) listener();
    },
    listeners,
    heights,
  };
}

// Observer stands in for ResizeObserver, keeping the last one made.
class Observer {
  static last: Observer | undefined;
  observed: unknown[] = [];
  disconnected = false;
  constructor(readonly resize: () => void) {
    Observer.last = this;
  }
  observe(target: unknown): void {
    this.observed.push(target);
  }
  disconnect(): void {
    this.disconnected = true;
  }
}

describe("message body frame", () => {
  let states: FrameState[];
  const report = (state: FrameState) => states.push(state);

  beforeEach(() => {
    states = [];
    Observer.last = undefined;
    vi.useFakeTimers();
    vi.stubGlobal("ResizeObserver", Observer);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("shows the body once it is read, before its images load", () => {
    const shown = frame(page({ url: "about:blank" }));
    watch(shown, report);

    vi.advanceTimersByTime(500);
    shown.contentDocument = page({ readyState: "loading" });
    vi.advanceTimersByTime(500);
    expect(states).toEqual([]);

    shown.contentDocument = page({ readyState: "interactive" });
    vi.advanceTimersByTime(100);
    expect(states).toEqual(["shown"]);
    // Collapsed first, so the frame can also shrink.
    expect(shown.heights).toEqual(["0", "900px"]);
    expect(Observer.last?.observed).toEqual([shown.contentDocument.documentElement]);
  });

  it("lets the body's root and body be as tall as their content", () => {
    const shown = frame(page());
    watch(shown, report);
    vi.advanceTimersByTime(100);

    for (const part of [shown.contentDocument?.documentElement, shown.contentDocument?.body])
      expect(part?.styles).toEqual({ height: "auto !important", "min-height": "0 !important" });
  });

  it("follows the body's height after it is shown", () => {
    const shown = frame(page());
    watch(shown, report);
    vi.advanceTimersByTime(100);

    const doc = shown.contentDocument;
    if (!doc) throw new Error("no document");
    doc.documentElement.scrollHeight = 1400;
    doc.documentElement.clientHeight = 1400;
    Observer.last?.resize();

    expect(shown.heights.slice(-2)).toEqual(["0", "1400px"]);
  });

  it("makes room for a horizontal scroll bar", () => {
    const wide = frame(page({ scroll: 900, client: 885, inner: 900 }));
    watch(wide, report);
    vi.advanceTimersByTime(100);

    expect(wide.heights.at(-1)).toBe("915px");
  });

  it("fails on an error answer", () => {
    // Refused a frame: a document this page cannot read.
    const refused = frame(page({ url: "about:blank" }));
    watch(refused, report);
    refused.contentDocument = null;
    refused.load();
    // Shown as text.
    const json = frame(page({ contentType: "application/problem+json" }));
    watch(json, report);
    vi.advanceTimersByTime(100);

    expect(states).toEqual(["failed", "failed"]);
    expect(refused.listeners.size).toBe(0);
  });

  it("fails when the body is never read", () => {
    const stuck = frame(page({ readyState: "loading" }));
    watch(stuck, report);

    vi.advanceTimersByTime(14_000);
    expect(states).toEqual([]);
    vi.advanceTimersByTime(2_000);
    expect(states).toEqual(["failed"]);
  });

  it("fails when another page replaces the body", () => {
    const shown = frame(page());
    watch(shown, report);
    vi.advanceTimersByTime(100);

    shown.load();
    expect(states).toEqual(["shown"]);
    shown.contentDocument = null;
    shown.load();
    expect(states).toEqual(["shown", "failed"]);
    expect(Observer.last?.disconnected).toBe(true);
  });

  it("stops watching when asked", () => {
    const shown = frame(page({ readyState: "loading" }));
    const stop = watch(shown, report);

    stop();
    vi.advanceTimersByTime(20_000);
    expect(states).toEqual([]);
    expect(shown.listeners.size).toBe(0);
  });
});
