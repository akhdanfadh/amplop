import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { getListConnectedAccountsQueryKey, useListConnectedAccounts } from "@/api/generated/inbok";
import { accountName } from "@/components/account-mark";
import { listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { AccountNameForm } from "./name-form";

let api: MailApi;

// Page shows the first account's name as the accounts list gives it, and
// its name form.
function Page() {
  const account = useListConnectedAccounts().data?.data.accounts[0];
  if (!account) return null;
  return (
    <>
      <p data-testid="listed">{accountName(account)}</p>
      <AccountNameForm account={account} onUnauthorized={() => {}} />
    </>
  );
}

// Fetch is how the page fetches; Wrap changes what the fake API answers,
// given the fake's own answer, next.
type Fetch = MailApi["fetch"];
type Wrap = (next: Fetch) => Fetch;

// open renders the name form of ana@work.example, named name, under the
// name the accounts list gives it, in a router, as Connected accounts
// does, with wrap's answers, and returns its query client.
async function open(name: string | null = null, wrap: Wrap = (next) => next) {
  api = mailApi([], [{ ...listedAccount(2, "ana@work.example"), name }]);
  vi.stubGlobal("fetch", vi.fn(wrap(api.fetch)));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({ component: Page });
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ["/"] }),
    Wrap: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  await render(<RouterProvider router={router} />);
  return client;
}

// refusal returns a Problem Details answer of status with more members.
function refusal(status: number, more: Record<string, unknown> = {}): Response {
  return Response.json(
    { type: "about:blank", status, title: "Failed", detail: "It failed.", ...more },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// refuseRename answers every rename with answer.
function refuseRename(answer: () => Response): Wrap {
  return (next) => async (input, init) => (init?.method === "PATCH" ? answer() : next(input, init));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the account name field", () => {
  it("saves a name, which the accounts list then shows", async () => {
    await open();
    const field = page.getByRole("textbox", { name: "Name (optional)" });

    await userEvent.fill(field, "  Work  ");
    await userEvent.click(page.getByRole("button", { name: "Save name" }));

    await expect.element(page.getByText("Saved")).toBeVisible();
    // The server keeps the name without the white space around it.
    await expect.element(field).toHaveValue("Work");
    expect(api.accounts[0].name).toBe("Work");
    await expect.element(page.getByTestId("listed")).toHaveTextContent("Work");
  });

  it("goes back to the address when emptied", async () => {
    await open("Work");

    await userEvent.fill(page.getByRole("textbox", { name: "Name (optional)" }), "");
    await userEvent.click(page.getByRole("button", { name: "Save name" }));

    await expect.element(page.getByTestId("listed")).toHaveTextContent("ana@work.example");
    expect(api.accounts[0].name).toBeNull();
  });

  it("stops a name at the length the server keeps", async () => {
    await open();

    await expect
      .element(page.getByRole("textbox", { name: "Name (optional)" }))
      .toHaveAttribute("maxlength", "64");
  });

  it("marks the field with the server's refusal of the name", async () => {
    await open(
      null,
      refuseRename(() =>
        refusal(422, {
          type: "/api/v0/problems/validation-error",
          errors: [{ location: "body.name", message: "Use another name." }],
        }),
      ),
    );
    const field = page.getByRole("textbox", { name: "Name (optional)" });

    await userEvent.fill(field, "Work");
    await userEvent.click(page.getByRole("button", { name: "Save name" }));

    await expect.element(field).toHaveAttribute("aria-invalid", "true");
    await expect.element(field).toHaveAccessibleDescription(/Use another name\./);
  });

  it("says why the name could not be saved, and keeps it", async () => {
    await open(
      null,
      refuseRename(() => refusal(500)),
    );
    const field = page.getByRole("textbox", { name: "Name (optional)" });

    await userEvent.fill(field, "Work");
    await userEvent.click(page.getByRole("button", { name: "Save name" }));

    await expect.element(page.getByRole("alert")).toBeVisible();
    await expect.element(field).toHaveValue("Work");
    expect(page.getByText("Saved").elements()).toHaveLength(0);
    await expect.element(page.getByTestId("listed")).toHaveTextContent("ana@work.example");
  });

  it("keeps the saved name when an older read of the accounts lands after it", async () => {
    // A read of the accounts while held waits to answer, with the name
    // it read before the rename.
    let held: Promise<void> | undefined;
    let release: (() => void) | undefined;
    const client = await open(null, (next) => async (input, init) => {
      const url = new URL(input instanceof Request ? input.url : String(input), location.href);
      const read =
        url.pathname === "/api/v0/connected-accounts" && (init?.method ?? "GET") === "GET";
      const answer = await next(input, init);
      if (read && held) await held;
      return answer;
    });
    const listed = page.getByTestId("listed");
    await expect.element(listed).toHaveTextContent("ana@work.example");
    held = new Promise((resolve) => {
      release = resolve;
    });
    void client.refetchQueries({ queryKey: getListConnectedAccountsQueryKey() });

    await userEvent.fill(page.getByRole("textbox", { name: "Name (optional)" }), "Work");
    await userEvent.click(page.getByRole("button", { name: "Save name" }));
    await expect.element(page.getByText("Saved")).toBeVisible();
    release?.();
    held = undefined;
    await new Promise((resolve) => setTimeout(resolve, 50));

    expect(listed.element().textContent).toBe("Work");
  });
});
