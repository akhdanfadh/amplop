import { QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { getListConnectedAccountsQueryKey, useListConnectedAccounts } from "@/api/generated/amplop";
import { accountName } from "@/components/account-mark";
import { listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { testQueryClient } from "@/test/query-client";
import { held, problem, type Server } from "@/test/server";
import { NameForm } from "./name-form";

const invalid = 422;
const failed = 500;

let api: MailApi;
// How many times the form asked to close.
let closes = 0;

// Page shows the first account's name as the accounts list gives it, and
// its name form.
function Page() {
  const account = useListConnectedAccounts().data?.data.accounts[0];
  if (!account) return null;
  return (
    <>
      <p data-testid="listed">{accountName(account)}</p>
      <NameForm account={account} onClose={() => (closes += 1)} />
    </>
  );
}

// open renders the name form of ana@work.example, named name, under the
// name the accounts list gives it, as Connected accounts does. arrange
// gets the fake server before the first request, to add its own routes.
// It returns the query client.
async function open(name: string | null = null, arrange: (server: Server) => void = () => {}) {
  closes = 0;
  api = mailApi([], [{ ...listedAccount(2, "ana@work.example"), name }]);
  arrange(api.server);
  api.server.install();
  const client = testQueryClient();
  await render(
    <QueryClientProvider client={client}>
      <Page />
    </QueryClientProvider>,
  );
  return client;
}

// refuseRename returns an arrangement whose server answers every rename
// with answer's Response.
function refuseRename(answer: () => Response): (server: Server) => void {
  return (server) => server.on("PATCH", "/api/v0/connected-accounts/{id}", answer);
}

const field = () => page.getByRole("textbox", { name: /^Name/ });
const save = () => page.getByRole("button", { name: "Save" });

describe("the account name form", () => {
  it("saves a name, which the accounts list then shows, and closes", async () => {
    await open();

    await userEvent.fill(field(), "  Work  ");
    await userEvent.click(save());

    // The server keeps the name without the white space around it.
    await expect.element(page.getByTestId("listed")).toHaveTextContent("Work");
    expect(api.accounts[0].name).toBe("Work");
    expect(closes).toBe(1);
  });

  it("opens with the name selected, so typing replaces it", async () => {
    await open("Work");

    await userEvent.keyboard("Home");

    expect(api.accounts[0].name).toBe("Work");
    await expect.element(field()).toHaveValue("Home");
  });

  it("goes back to the address when emptied", async () => {
    await open("Work");

    await userEvent.fill(field(), "");
    await userEvent.click(save());

    await expect.element(page.getByTestId("listed")).toHaveTextContent("ana@work.example");
    expect(api.accounts[0].name).toBeNull();
  });

  it("says a name is too long instead of cutting it", async () => {
    await open();

    // DESIGN.md "Forms": no maxlength, which would cut a paste unseen.
    await expect.element(field()).not.toHaveAttribute("maxlength");
    await userEvent.fill(field(), "W".repeat(65));
    await userEvent.click(save());

    await expect.element(field()).toHaveAttribute("aria-invalid", "true");
    await expect.element(field()).toHaveAccessibleDescription(/64/);
    expect(api.accounts[0].name).toBeNull();
    expect(closes).toBe(0);
  });

  it("marks the field with the server's refusal of the name", async () => {
    await open(
      null,
      refuseRename(() =>
        problem(invalid, "/api/v0/problems/validation-error", {
          errors: [{ location: "body.name", message: "Use another name." }],
        }),
      ),
    );

    await userEvent.fill(field(), "Work");
    await userEvent.click(save());

    await expect.element(field()).toHaveAttribute("aria-invalid", "true");
    await expect.element(field()).toHaveAccessibleDescription(/Use another name\./);
    expect(closes).toBe(0);
  });

  it("says why the name could not be saved, and stays open with it", async () => {
    await open(
      null,
      refuseRename(() => problem(failed)),
    );

    await userEvent.fill(field(), "Work");
    await userEvent.click(save());

    await expect.element(page.getByRole("alert")).toBeVisible();
    await expect.element(field()).toHaveValue("Work");
    await expect.element(page.getByTestId("listed")).toHaveTextContent("ana@work.example");
    expect(closes).toBe(0);
  });

  it("closes without saving on Cancel or Escape", async () => {
    await open();

    await userEvent.fill(field(), "Work");
    await userEvent.keyboard("{Escape}");
    expect(closes).toBe(1);
    await userEvent.click(page.getByRole("button", { name: "Cancel" }));
    expect(closes).toBe(2);
    expect(api.accounts[0].name).toBeNull();
  });

  it("keeps the saved name when an older read of the accounts lands after it", async () => {
    // A read of the accounts while held waits to answer, with the name
    // it read before the rename.
    let gate: ReturnType<typeof held> | undefined;
    const client = await open(null, (server) =>
      server.on("GET", "/api/v0/connected-accounts", async (_, earlier) => {
        const answer = await earlier();
        if (gate) await gate.promise;
        return answer;
      }),
    );
    const listed = page.getByTestId("listed");
    await expect.element(listed).toHaveTextContent("ana@work.example");
    gate = held();
    const older = client.refetchQueries({ queryKey: getListConnectedAccountsQueryKey() });

    await userEvent.fill(field(), "Work");
    await userEvent.click(save());
    await expect.element(listed).toHaveTextContent("Work");
    gate.resolve();
    gate = undefined;
    // The older read has ended, and so has any read begun since.
    await older;
    await expect.poll(() => client.isFetching()).toBe(0);

    // The list draws what the client holds by the frame after next, so
    // the older read's answer would show here.
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    expect(listed.element().textContent).toBe("Work");
  });
});
