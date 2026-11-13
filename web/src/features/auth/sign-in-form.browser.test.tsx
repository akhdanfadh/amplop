import "@/zod-config";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { userEvent, page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CatchBoundary } from "@tanstack/react-router";
import { SignInForm } from "./sign-in-form";

const unauthorized = 401;

// signedIn answers a sign-in with step: "complete", or "totp" when the
// account needs its authenticator code next.
function signedIn(step: "complete" | "totp"): Response {
  return new Response(JSON.stringify({ data: { step } }), {
    status: 201,
    headers: { "Content-Type": "application/json" },
  });
}

// refused answers a sign-in whose email or password is wrong.
function refused(): Response {
  const problem = { type: "/api/v0/problems/invalid-credentials", status: unauthorized };
  return new Response(JSON.stringify({ ...problem, title: "Invalid credentials" }), {
    status: unauthorized,
    headers: { "Content-Type": "application/problem+json" },
  });
}

// signIn renders the form in Strict Mode, as the app runs in development,
// with fetch held until answer.send is called; it fills the form and
// submits. It returns the callbacks, the answer, and the rendered screen.
async function signIn() {
  const answer: { send?: (response: Response) => void } = {};
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(() => new Promise((resolve) => (answer.send = resolve))),
  );
  const client = new QueryClient();
  const onDone = vi.fn<() => void>();
  const onCodeRequired = vi.fn<() => void>();
  const screen = await render(
    <StrictMode>
      <CatchBoundary getResetKey={() => 0} errorComponent={() => <p>Crashed</p>}>
        <QueryClientProvider client={client}>
          <SignInForm onDone={onDone} onCodeRequired={onCodeRequired} />
        </QueryClientProvider>
      </CatchBoundary>
    </StrictMode>,
  );
  await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");
  await userEvent.type(page.getByLabelText("Password", { exact: true }), "a long password");
  await userEvent.click(page.getByRole("button", { name: "Sign in" }));
  await vi.waitFor(() => expect(answer.send).toBeDefined());
  return { answer, client, onDone, onCodeRequired, screen };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("SignInForm", () => {
  it.each([
    ["complete", "onDone"],
    ["totp", "onCodeRequired"],
  ] as const)("reports a %s sign-in once", async (step, callback) => {
    const signedInWith = await signIn();

    signedInWith.answer.send?.(signedIn(step));

    await vi.waitFor(() => expect(signedInWith[callback]).toHaveBeenCalledTimes(1));
  });

  it.each(["complete", "totp"] as const)(
    "ignores a %s sign-in that answers after the member left the form",
    async (step) => {
      const { answer, client, onDone, onCodeRequired, screen } = await signIn();

      // The member follows "Forgot password?" while the request runs.
      await screen.unmount();
      answer.send?.(signedIn(step));
      await vi.waitFor(() => expect(client.isMutating()).toBe(0));
      // The awaited code resumes a task after the request settles.
      await new Promise((resolve) => setTimeout(resolve, 0));

      expect(onDone).not.toHaveBeenCalled();
      expect(onCodeRequired).not.toHaveBeenCalled();
    },
  );

  it("shows a refused sign-in under the button, not on the crash screen", async () => {
    const { answer } = await signIn();

    answer.send?.(refused());

    await expect.element(page.getByRole("alert")).toBeVisible();
    await expect.element(page.getByText("Crashed")).not.toBeInTheDocument();
  });
});
