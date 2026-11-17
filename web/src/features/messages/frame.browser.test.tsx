import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { messagePath, sessionCookie } from "@/test/message-server";
import { sandbox, watch, type FrameState } from "./frame";

// The frames a test made, with what stops watching each.
const mounted: { frame: HTMLIFrameElement; stop: () => void }[] = [];

afterEach(() => {
  for (const { frame, stop } of mounted.splice(0)) {
    stop();
    frame.remove();
  }
});

// show watches a frame width pixels wide showing the test body name, as
// the message view does, and returns it with the states it reported.
function show(name: string, width = 480) {
  const frame = document.createElement("iframe");
  frame.sandbox.value = sandbox;
  frame.style.width = `${width}px`;
  frame.style.border = "0";
  frame.style.display = "block";
  frame.src = messagePath + name;
  const states: FrameState[] = [];
  document.body.append(frame);
  mounted.push({ frame, stop: watch(frame, (state) => states.push(state)) });
  return { frame, states };
}

// shown waits until frame shows its body, and returns the body's root.
async function shown(view: ReturnType<typeof show>): Promise<HTMLElement> {
  await expect.poll(() => view.states).toEqual(["shown"]);
  const root = view.frame.contentDocument?.documentElement;
  if (!root) throw new Error("the frame shows no document");
  return root;
}

function zoom(root: HTMLElement): number {
  return Number(getComputedStyle(root).zoom);
}

describe("watch", () => {
  it("fits the frame to the body, which runs no script", async () => {
    const view = show("script");
    const root = await shown(view);

    // The body asks for 100% of the frame plus padding, which would never
    // fit; it is as tall as its text instead.
    expect(view.frame.offsetHeight).toBe(root.scrollHeight);
    expect(view.frame.offsetHeight).toBeLessThan(150);
    expect(Reflect.get(window, "scriptRan")).toBeUndefined();
  });

  it("keeps mail that fits at its full size", async () => {
    const root = await shown(show("fluid"));

    expect(zoom(root)).toBe(1);
  });

  it("scales mail wider than the frame to fit it", async () => {
    const view = show("wide600");
    const root = await shown(view);

    // 600 px of table plus the body's 8 px margin, in 480 px.
    expect(zoom(root)).toBeCloseTo(480 / 608, 2);
    expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);
    expect(view.frame.offsetHeight).toBe(root.scrollHeight);
  });

  it("scrolls mail too wide for 60% sideways, inside the frame", async () => {
    const view = show("wide1200");
    const root = await shown(view);

    expect(zoom(root)).toBe(0.6);
    expect(root.scrollWidth).toBeGreaterThan(root.clientWidth);
    // NOTE: headless Chromium hides scroll bars, so the room fit makes for
    // one below the body is seen only in a headed browser.
    expect(view.frame.offsetHeight).toBe(root.scrollHeight);
  });

  it("scrolls mail too wide for 60% sideways, even when it hides its overflow", async () => {
    const view = show("hiddenOverflow");
    const root = await shown(view);

    expect(zoom(root)).toBe(0.6);
    expect(root.scrollWidth).toBeGreaterThan(root.clientWidth);
    // A script could scroll a hidden overflow; a member could not.
    expect(getComputedStyle(root).overflowX).toBe("auto");
  });

  it("refits mail that fixes its root's width when the frame changes", async () => {
    const view = show("fixedRoot");
    const root = await shown(view);
    expect(zoom(root)).toBeCloseTo(0.8, 2);

    view.frame.style.width = "700px";

    await expect.poll(() => zoom(root)).toBe(1);
  });

  it("shows a body while one of its images never arrives", async () => {
    const root = await shown(show("slowImage"));

    // The load event, which waits for every image, has not come.
    expect(root.querySelector("img")?.complete).toBe(false);
  });

  it("refits mail an image widens after it is shown", async () => {
    const root = await shown(show("lateBanner"));
    expect(zoom(root)).toBe(1);

    await fetch(`${messagePath}release-late`);

    await expect.poll(() => zoom(root)).toBe(0.6);
  });

  it("returns scaled mail to full size when the frame widens", async () => {
    const view = show("wide600");
    const root = await shown(view);

    view.frame.style.width = "700px";

    await expect.poll(() => zoom(root)).toBe(1);
  });

  it("grows the frame as the body grows", async () => {
    const view = show("details");
    const root = await shown(view);
    const closed = view.frame.offsetHeight;

    root.querySelector("details")?.setAttribute("open", "");

    await expect.poll(() => view.frame.offsetHeight).toBeGreaterThan(closed + 400);
  });

  it("shrinks the frame as the body shrinks", async () => {
    const view = show("details");
    const root = await shown(view);
    const closed = view.frame.offsetHeight;
    const details = root.querySelector("details");
    details?.setAttribute("open", "");
    await expect.poll(() => view.frame.offsetHeight).toBeGreaterThan(closed);

    details?.removeAttribute("open");

    await expect.poll(() => view.frame.offsetHeight).toBe(closed);
  });

  it("loads an inline image with the session cookie", async () => {
    // Strict, as inbok's session cookie is: a frame of another origin would
    // not send it.
    document.cookie = `${sessionCookie}; path=/; SameSite=Strict`;
    const root = await shown(show("image"));
    const image = root.querySelector("img");

    await expect.poll(() => image?.complete && image.naturalWidth).toBe(1);
  });

  it("fails on an error answer, which the frame is refused", async () => {
    const view = show("error");

    await expect.poll(() => view.states).toEqual(["failed"]);
  });

  it("fails on a body that never finishes", async () => {
    // Only the clock: the frame's polling keeps its real timer.
    vi.useFakeTimers({ toFake: ["Date"] });
    const view = show("stall");
    await expect.poll(() => view.frame.contentDocument?.body?.textContent).toContain("start");
    expect(view.states).toEqual([]);

    vi.setSystemTime(Date.now() + 16_000);

    await expect.poll(() => view.states).toEqual(["failed"]);
  });

  it("fails when another page replaces the body", async () => {
    const view = show("fluid");
    await shown(view);

    view.frame.src = `${messagePath}details`;

    await expect.poll(() => view.states).toEqual(["shown", "failed"]);
  });
});
