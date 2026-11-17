import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { RouterProvider } from "@tanstack/react-router";
import type { ListedMembers, Member } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import {
  adminSession,
  data,
  fakeServer,
  held,
  lose,
  problem,
  type Asked,
  type Server,
} from "@/test/server";

const notFound = 404;
const fault = 500;
const membersPath = "/api/v0/members";
// A phone's window, and a desktop's, in CSS pixels.
const phone = { width: 360, height: 640 };
const desktop = { width: 1280, height: 720 };

// member returns an active member the server lets an administrator change.
function member(id: number, email: string, changes: Partial<Member> = {}): Member {
  return {
    id,
    email,
    role: "member",
    joinedAt: "2026-03-01T09:00:00Z",
    emailVerified: true,
    totpEnabled: false,
    deactivatedAt: null,
    allowedActions: ["deactivate", "reactivate"],
    ...changes,
  };
}

const admin = member(1, adminSession.email, {
  role: "admin",
  totpEnabled: true,
  allowedActions: [],
});

// Api is the fake server and the members it holds.
type Api = { server: Server; members: Member[] };

// api returns a fake server for an administrator, which holds members and
// deactivates or reactivates one when asked.
function api(members: Member[]): Api {
  const server = fakeServer(adminSession);
  const state: Api = { server, members };
  server.on("GET", "/api/v0/members", () => {
    const listed: ListedMembers = { members: state.members };
    return data(listed);
  });
  const change = (id: string, deactivatedAt: string | null) => {
    const target = state.members.find((m) => m.id === Number(id));
    if (!target)
      return problem(notFound, "/api/v0/problems/member-not-found", {
        detail: "This account doesn't exist.",
      });
    const saved = { ...target, deactivatedAt };
    state.members = state.members.map((m) => (m.id === saved.id ? saved : m));
    return data({ member: saved });
  };
  server.on("POST", "/api/v0/members/{id}/deactivate", ({ params }) =>
    change(params.id, "2026-11-15T10:00:00Z"),
  );
  server.on("POST", "/api/v0/members/{id}/reactivate", ({ params }) => change(params.id, null));
  return state;
}

// serverFault is a server fault, as the API words one.
const serverFault = () =>
  problem(fault, "/api/v0/problems/internal-error", {
    detail: "Something went wrong on our side.",
  });

// sent lists requests as e.g. "GET /api/v0/members".
const sent = (requests: Asked[]) =>
  requests.map((request) => `${request.method} ${request.url.pathname}`);

// The router of the page open now, whose reads reread marks.
let opened: Awaited<ReturnType<typeof appRouter>> | undefined;

// open renders the app at Members in a window of size, a phone's when
// left out, its requests answered by server, and returns its router.
async function open(server: Server, size = phone) {
  await page.viewport(size.width, size.height);
  const router = await appRouter("/settings/members", server);
  opened = router;
  await render(<RouterProvider router={router} />);
  return router;
}

// reread marks every read out of date, so the page reads its own again,
// as it does e.g. on returning to the window.
async function reread(): Promise<void> {
  await opened?.options.context.queryClient.invalidateQueries();
}

const button = (name: string) => page.getByRole("button", { name, exact: true });
const dialog = () => page.getByRole("alertdialog");
const announcement = () => page.getByRole("status").last();

// confirmByKeyboard presses the dialog's confirming button: Cancel has
// focus, the action is next. A keyboard press counts at once, unlike a click
// within a double-click of the dialog opening.
async function confirmByKeyboard(): Promise<void> {
  await expect.element(dialog()).toBeVisible();
  await userEvent.keyboard("{Tab}{Enter}");
}

// fade returns the member list's fade on side, "right" or "below", if shown.
function fade(side: "right" | "below"): Element | null {
  return document.querySelector(`[data-slot=more-${side}]`);
}

// frames waits for the browser to draw twice, e.g. after a scroll.
function frames(): Promise<void> {
  return new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done())));
}

describe("Members", () => {
  const jo = member(2, "jo@example.com");
  const sam = member(3, "sam@example.com", {
    deactivatedAt: "2026-11-02T09:30:00Z",
    emailVerified: false,
  });
  // The server, not the page, decides whom an administrator can change.
  const kept = member(4, "kept@example.com", { allowedActions: [] });

  it("marks only what differs and offers what the server allows", async () => {
    const { server } = api([admin, jo, sam, kept]);
    await open(server);

    await expect.element(page.getByText("You", { exact: true })).toBeVisible();
    await expect.element(page.getByText("Admin", { exact: true })).toBeVisible();
    await expect.element(page.getByText("Not verified", { exact: true })).toBeVisible();
    await expect.element(page.getByText("Deactivated", { exact: true })).toBeVisible();
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    await expect.element(button("Reactivate sam@example.com")).toBeVisible();
    // The page uses what its loader just read rather than reading it again.
    await frames();
    expect(
      sent(server.requests).filter((request) => request === `GET ${membersPath}`),
    ).toHaveLength(1);
    // Neither the administrator nor a member the server keeps has a button.
    // In the page: the frame's account menu is named for the member too.
    expect(
      page
        .getByRole("main")
        .getByRole("button", { name: /ana@example\.com|kept@example\.com/ })
        .all(),
    ).toEqual([]);
  });

  it("holds a change in its dialog, then returns focus to the row", async () => {
    const save = held();
    const { server } = api([admin, jo]);
    server.on("POST", "/api/v0/members/{id}/deactivate", async (_, earlier) => {
      await save.promise;
      return earlier();
    });
    await open(server);
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();

    await userEvent.click(button("Deactivate jo@example.com"));
    // Heard whole, though the address is split for line breaks.
    await expect
      .element(page.getByRole("alertdialog", { name: "Deactivate jo@example.com?" }))
      .toBeVisible();
    await confirmByKeyboard();
    await expect.element(button("Deactivating...")).toBeVisible();
    await expect.element(button("Cancel")).toBeDisabled();

    save.resolve();
    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(button("Reactivate jo@example.com")).toHaveFocus();
    await expect.element(page.getByText("Deactivated", { exact: true })).toBeVisible();
    await expect.element(announcement()).toHaveTextContent("jo@example.com is deactivated.");
  });

  it("shows a failure in the dialog, where pressing again retries", async () => {
    const state = api([admin, jo]);
    state.server.once("POST", "/api/v0/members/{id}/deactivate", serverFault);
    await open(state.server);

    await userEvent.click(button("Deactivate jo@example.com"));
    await confirmByKeyboard();
    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent(
        "Something went wrong on our side. Couldn't confirm whether jo@example.com was deactivated. Trying again is safe.",
      );

    await userEvent.keyboard("{Enter}");
    await expect.element(dialog()).not.toBeInTheDocument();
    expect(state.members.find((m) => m.id === jo.id)?.deactivatedAt).not.toBeNull();
  });

  it("says trying again is safe when the answer is lost", async () => {
    const { server } = api([admin, jo]);
    // The server deactivates, and its answer is lost.
    server.once("POST", "/api/v0/members/{id}/deactivate", async (_, earlier) => {
      await earlier();
      return lose();
    });
    await open(server);

    await userEvent.click(button("Deactivate jo@example.com"));
    await confirmByKeyboard();

    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent(
        "Couldn't confirm whether jo@example.com was deactivated. Trying again is safe.",
      );
  });

  it("keeps the list and says so when a refresh fails", async () => {
    const { server } = api([admin, jo]);
    await open(server);

    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    // The page's own reread, e.g. on returning to the window, fails.
    server.on("GET", "/api/v0/members", serverFault);
    await reread();
    await expect.element(page.getByText(/Couldn't refresh the member list/)).toBeVisible();
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
  });

  it("scrolls the list in its own box, header and member column held", async () => {
    const many = Array.from({ length: 20 }, (_, i) => member(10 + i, `member${i}@example.com`));
    const { server } = api([admin, ...many]);
    await open(server);
    await expect.element(button("Deactivate member19@example.com")).toBeInTheDocument();
    // With the refresh banner above it, the list keeps the height left.
    server.on("GET", "/api/v0/members", serverFault);
    await reread();
    await expect.element(page.getByText(/Couldn't refresh the member list/)).toBeVisible();
    const region = page.getByRole("region", { name: "Member list" });
    await expect.element(region).toHaveAttribute("tabindex", "0");
    const box = region.element();
    const memberHead = page.getByRole("columnheader", { name: "Member" }).element();
    const statusHead = page.getByRole("columnheader", { name: "Status" }).element();
    const firstRow = page.getByRole("rowheader").first().element();
    const statusBefore = statusHead.getBoundingClientRect().left;
    // The page fits the frame: the list overflows its box, not the page.
    expect(box.scrollHeight).toBeGreaterThan(box.clientHeight);
    expect(box.scrollWidth).toBeGreaterThan(box.clientWidth);

    box.scrollTop = 200;
    box.scrollLeft = 120;
    await frames();

    const top = box.getBoundingClientRect().top;
    const left = box.getBoundingClientRect().left;
    expect(memberHead.getBoundingClientRect().top).toBeCloseTo(top, 0);
    expect(statusHead.getBoundingClientRect().top).toBeCloseTo(top, 0);
    expect(memberHead.getBoundingClientRect().left).toBeCloseTo(left, 0);
    expect(firstRow.getBoundingClientRect().left).toBeCloseTo(left, 0);
    expect(statusHead.getBoundingClientRect().left).toBeCloseTo(statusBefore - 120, 0);
    // The pinned column's edge shows only while the columns are scrolled.
    expect(box.hasAttribute("data-scrolled")).toBe(true);
    // Fades mark the columns to the right and the rows below until the
    // last of each is in view.
    expect(fade("right")).not.toBeNull();
    expect(fade("below")).not.toBeNull();
    box.scrollLeft = box.scrollWidth;
    box.scrollTop = box.scrollHeight;
    await frames();
    expect(fade("right")).toBeNull();
    expect(fade("below")).toBeNull();
    // Scrolling back up to a button stops below the header, not under it.
    box.scrollTop = box.scrollHeight;
    await frames();
    const earlier = button("Deactivate member3@example.com").element();
    // As Shift+Tab scrolls a button into view: to the nearest edge.
    earlier.scrollIntoView({ block: "nearest" });
    await frames();
    expect(earlier.getBoundingClientRect().top).toBeGreaterThanOrEqual(
      memberHead.getBoundingClientRect().bottom - 1,
    );
    // Scrolling down to a button stops above the bottom fade.
    box.scrollTop = 0;
    await frames();
    const later = button("Deactivate member15@example.com").element();
    later.scrollIntoView({ block: "nearest" });
    await frames();
    expect(later.getBoundingClientRect().bottom).toBeLessThanOrEqual(
      box.getBoundingClientRect().bottom - 32 + 1,
    );
    // A row's action still answers a press.
    await userEvent.click(button("Deactivate member19@example.com"));
    await expect.element(dialog()).toBeVisible();
  });

  it("takes a Tab stop only while the list scrolls", async () => {
    await open(api([admin, jo]).server, desktop);
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    const box = document.querySelector("[data-slot=table-container]");

    // Wide and short enough to fit: nothing to scroll, so no Tab stop, no
    // region of its own, and no fade.
    expect(box?.hasAttribute("tabindex")).toBe(false);
    expect(box?.getAttribute("role")).toBeNull();
    expect(fade("right")).toBeNull();
    expect(fade("below")).toBeNull();
  });

  it("keeps an address with no break point inside its column", async () => {
    const long = member(5, `${"w".repeat(64)}@${"x".repeat(63)}.com`);
    await open(api([admin, long]).server);
    const cell = page.getByRole("rowheader").last();
    await expect.element(cell).toBeVisible();

    const element = cell.element();
    expect(element.scrollWidth).toBeLessThanOrEqual(element.clientWidth + 1);
  });
});
