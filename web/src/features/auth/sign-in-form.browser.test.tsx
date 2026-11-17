import "@/zod-config";
import { StrictMode } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { userEvent, page } from "vite-plus/test/browser/context";
import { CatchBoundary } from "@tanstack/react-router";
import type { SignInResult } from "@/api/generated/model";
import { data, fakeServer, held, problem } from "@/test/server";
import { stubRouter } from "@/test/stub-router";
import { SignInForm } from "./sign-in-form";

const created = 201;
const unauthorized = 401;

// signedIn answers a sign-in with step: "complete", or "totp" when the
// account needs its authenticator code next.
function signedIn(step: SignInResult["step"]): Response {
  const result: SignInResult = { step };
  return data(result, created);
}

// refused answers a sign-in whose email or password is wrong.
function refused(): Response {
  return problem(unauthorized, "/api/v0/problems/invalid-credentials");
}

// show renders the form in Strict Mode, as the app runs in development, on
// a page of its own (its "Forgot password?" is a router link), on a
// visitor's server that holds a sign-in until answer.send is called with
// its answer. It returns the callbacks, the answer, the server, the query
// client, and the rendered screen.
async function show() {
  const server = fakeServer().install();
  const sent = held();
  const answer: { response?: Response; send: (response: Response) => void } = {
    send: (response) => {
      answer.response = response;
      sent.resolve();
    },
  };
  server.on("POST", "/api/v0/sessions", async () => {
    await sent.promise;
    return answer.response ?? refused();
  });
  const onDone = vi.fn<() => void>();
  const onCodeRequired = vi.fn<() => void>();
  const { client, screen } = await stubRouter(
    <StrictMode>
      <CatchBoundary getResetKey={() => 0} errorComponent={() => <p>Crashed</p>}>
        <SignInForm onDone={onDone} onCodeRequired={onCodeRequired} />
      </CatchBoundary>
    </StrictMode>,
  );
  return { answer, server, client, onDone, onCodeRequired, screen };
}

const email = () => page.getByRole("textbox", { name: "Email address" });
const password = () => page.getByLabelText("Password", { exact: true });

// top returns where element's box starts, in CSS pixels from the viewport.
function top(element: Element): number {
  return element.getBoundingClientRect().top;
}

// signIn shows the form, fills it, and submits, as show returns.
async function signIn() {
  const shown = await show();
  await userEvent.type(email(), "ana@example.com");
  await userEvent.type(password(), "a long password");
  await userEvent.click(page.getByRole("button", { name: "Sign in" }));
  await vi.waitFor(() =>
    expect(shown.server.requests.map((asked) => `${asked.method} ${asked.path}`)).toContain(
      "POST /api/v0/sessions",
    ),
  );
  return shown;
}

describe("SignInForm", () => {
  it('puts "Forgot password?" on the label\'s line, after the password in Tab order', async () => {
    await show();
    const forgot = page.getByRole("link", { name: "Forgot password?" });

    await userEvent.click(email());
    await userEvent.tab();
    await expect.element(password()).toHaveFocus();
    await userEvent.tab();
    await expect.element(page.getByRole("button", { name: "Show password" })).toHaveFocus();
    await userEvent.tab();

    await expect.element(forgot).toHaveFocus();
    const label = page.getByText("Password", { exact: true }).element();
    expect(Math.abs(top(forgot.element()) - top(label))).toBeLessThan(8);
  });

  it.each([
    ["complete", "onDone"],
    ["totp", "onCodeRequired"],
  ] as const)("reports a %s sign-in once", async (step, callback) => {
    const signedInWith = await signIn();

    signedInWith.answer.send(signedIn(step));

    await vi.waitFor(() => expect(signedInWith[callback]).toHaveBeenCalledTimes(1));
  });

  it.each(["complete", "totp"] as const)(
    "ignores a %s sign-in that answers after the member left the form",
    async (step) => {
      const { answer, client, onDone, onCodeRequired, screen } = await signIn();

      // The member follows "Forgot password?" while the request runs.
      await screen.unmount();
      answer.send(signedIn(step));
      await vi.waitFor(() => expect(client.isMutating()).toBe(0));
      // The awaited code resumes a task after the request settles.
      await new Promise((resolve) => setTimeout(resolve, 0));

      expect(onDone).not.toHaveBeenCalled();
      expect(onCodeRequired).not.toHaveBeenCalled();
    },
  );

  it("shows a refused sign-in under the button, not on the crash screen", async () => {
    const { answer } = await signIn();

    answer.send(refused());

    await expect.element(page.getByRole("alert")).toBeVisible();
    await expect.element(page.getByText("Crashed")).not.toBeInTheDocument();
  });
});
