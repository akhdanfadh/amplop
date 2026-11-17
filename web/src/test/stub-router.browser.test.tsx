import { Link, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { stubRouter } from "./stub-router";
import { testQueryClient } from "./query-client";

// Leaver is a component with a link and a button that navigates.
function Leaver() {
  const navigate = useNavigate();
  return (
    <>
      <Link to="/sign-in">Sign in</Link>
      <button type="button" onClick={() => void navigate({ href: "/nowhere" })}>
        Go nowhere
      </button>
    </>
  );
}

describe("stubRouter", () => {
  it("renders the component at the address, with its search, and a blank page to leave to", async () => {
    const { router } = await stubRouter(<Leaver />, {
      at: "/mail/inbox?q=lunch#top",
      pages: ["/sign-in"],
    });

    expect(router.state.location.pathname).toBe("/mail/inbox");
    expect(router.state.location.search).toEqual({ q: "lunch" });
    await userEvent.click(page.getByRole("link", { name: "Sign in" }));

    await expect.element(page.getByRole("heading", { name: "/sign-in", level: 1 })).toBeVisible();
    await expect.element(page.getByRole("link", { name: "Sign in" })).not.toBeInTheDocument();
  });

  it("puts the layout around every page, and leaves the address blank without a component", async () => {
    const { router } = await stubRouter(null, {
      at: "/settings/security",
      pages: ["/", "/settings/security"],
      layout: (shown) => <section aria-label="Frame">{shown}</section>,
    });
    const frame = page.getByRole("region", { name: "Frame" });

    await expect.element(frame.getByRole("heading", { name: "/settings/security" })).toBeVisible();
    await router.navigate({ href: "/" });

    await expect.element(frame.getByRole("heading", { name: "/", exact: true })).toBeVisible();
  });

  it("gives the component the test's query client, or one of its own", async () => {
    const seen: unknown[] = [];
    function Reads() {
      seen.push(useQueryClient());
      return null;
    }
    const own = testQueryClient();

    const first = await stubRouter(<Reads />, { client: own });
    expect(first.client).toBe(own);
    expect(seen.at(-1)).toBe(own);
    await first.screen.unmount();

    const second = await stubRouter(<Reads />);
    expect(second.client).not.toBe(own);
    expect(seen.at(-1)).toBe(second.client);
  });

  it("keeps the location of an address no page has", async () => {
    const { router } = await stubRouter(<Leaver />);

    await userEvent.click(page.getByRole("button", { name: "Go nowhere" }));

    await expect.poll(() => router.state.location.pathname).toBe("/nowhere");
  });
});
