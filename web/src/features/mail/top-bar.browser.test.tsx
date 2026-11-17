import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { Frame } from "@/components/frame";
import { conversation, listedAccount, mailApi } from "@/test/mail-api";
import { stubRouter } from "@/test/stub-router";
import { PlacePage, type PlaceAccount } from "./place-page";

const projects = "22222222-2222-4222-8222-222222222222";

// The place a test opens, as the place route gives it to its page: its
// name, its own name, the account it shows, and whether a conversation is
// open.
type Opened = {
  place?: string;
  name?: string;
  ownName?: string;
  account?: PlaceAccount;
  pane?: "list" | "reader";
};

// open renders the signed-in frame around a place's page, on a window
// width wide, with two accounts.
//
// DESIGN: not the whole app (test/app-router.ts), so each test gives the
// place's names, account, and pane directly rather than through its
// route's loader.
async function open(
  width: number,
  { place = "inbox", name = "Inbox", ownName = name, account, pane = "list" }: Opened = {},
) {
  await page.viewport(width, 800);
  const api = mailApi(
    [conversation(1)],
    [listedAccount(1, "ana@example.com"), listedAccount(2, "ana@work.example")],
  );
  api.places = [
    { accountId: 1, place: "inbox", name: null, parent: null, unread: 3 },
    { accountId: 2, place: "inbox", name: null, parent: null, unread: 2 },
  ];
  api.server.install();
  await stubRouter(
    <div className="flex h-full flex-col">
      <PlacePage
        place={place}
        name={name}
        ownName={ownName}
        account={account}
        openId={undefined}
        pane={pane}
        reader={null}
      />
    </div>,
    {
      at: `/mail/${place}`,
      layout: (content) => (
        <Frame label="Mail" nav={null}>
          {content}
        </Frame>
      ),
    },
  );
}

// middle returns how far down the window element's middle is, in whole
// pixels.
function middle(element: Element): number {
  const box = element.getBoundingClientRect();
  return Math.round(box.top + box.height / 2);
}

describe("the top bar, where the sidebar is a drawer", () => {
  it("holds the place's heading and Check for new mail on a phone, not the counts", async () => {
    await open(390);

    const bar = page.getByRole("banner");
    await expect.element(bar.getByRole("heading", { level: 1, name: "Inbox" })).toBeVisible();
    await expect.element(bar.getByRole("button", { name: "Check for new mail" })).toBeVisible();
    // The list's bold rows show what is unread; the drawer has the counts.
    expect(bar.getByText(/unread/).elements()).toHaveLength(0);
    // The wordmark gives way to the place's own bar.
    expect(bar.getByRole("link", { name: "inbok" }).elements()).toHaveLength(0);
  });

  it("centres the heading between the buttons, at the drawer header's height", async () => {
    await open(390);

    const bar = page.getByRole("banner");
    const heading = bar.getByRole("heading", { level: 1, name: "Inbox" });
    await expect.element(heading).toBeVisible();
    const menu = bar.getByRole("button", { name: "Open sidebar" }).element();
    const check = bar.getByRole("button", { name: "Check for new mail" }).element();
    const top = menu.getBoundingClientRect().top;
    expect(middle(heading.element())).toBe(middle(menu));
    expect(middle(check)).toBe(middle(menu));

    // The drawer's header holds its button where the bar held this one.
    await userEvent.click(bar.getByRole("button", { name: "Open sidebar" }));
    const close = page.getByRole("dialog", { name: "Mail" }).getByRole("button", {
      name: "Close sidebar",
    });
    await expect.poll(() => close.element().getBoundingClientRect().top).toBe(top);
  });

  it("holds them in a wider window where the sidebar is still a drawer", async () => {
    await open(900);

    const bar = page.getByRole("banner");
    await expect.element(bar.getByRole("heading", { level: 1, name: "Inbox" })).toBeVisible();
    await expect.element(bar.getByRole("button", { name: "Check for new mail" })).toBeVisible();
  });

  it("shows a nested label by its own name, its path read to screen readers", async () => {
    await open(390, {
      place: projects,
      name: "Work/Projects",
      ownName: "Projects",
      account: { id: 1, color: 1, email: "ana@example.com", name: null },
    });

    const bar = page.getByRole("banner");
    await expect
      .element(bar.getByRole("heading", { level: 1, name: "Work/Projects" }))
      .toBeInTheDocument();
    await expect.element(bar.getByText("Projects", { exact: true })).toBeVisible();
    await expect.element(bar.getByText("ana@example.com")).toBeVisible();
  });

  it("leaves the bar to the wordmark while a conversation is open on a phone", async () => {
    await open(390, { pane: "reader" });

    const bar = page.getByRole("banner");
    await expect.element(bar.getByRole("link", { name: "inbok" })).toBeVisible();
    expect(bar.getByRole("heading", { level: 1, name: "Inbox" }).elements()).toHaveLength(0);
  });
});
