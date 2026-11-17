import "@/zod-config";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SignedOutPage } from "@/components/signed-out-page";
import type { ApiError } from "@/api/transport";
import { CodeForm } from "./code-form";

const unauthorized = 401;

// challengeExpired answers a code with the attempt already expired.
function challengeExpired(): Response {
  const problem = {
    type: "/api/v0/problems/sign-in-challenge-invalid",
    status: unauthorized,
    title: "Sign in again",
    detail: "Enter your email and password to start sign-in again.",
  };
  return Response.json(problem, {
    status: unauthorized,
    headers: { "Content-Type": "application/problem+json" },
  });
}

describe("CodeForm", () => {
  it("takes a code typed as soon as the step opens", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(() => new Promise(() => {})),
    );
    await render(
      <QueryClientProvider client={new QueryClient()}>
        {/* As the sign-in page shows it: the title takes focus first. */}
        <SignedOutPage title="Verify your sign-in" step="code">
          <CodeForm
            onDone={vi.fn<() => void>()}
            onEnded={vi.fn<(err: ApiError) => void>()}
            onRestart={vi.fn<() => void>()}
          />
        </SignedOutPage>
      </QueryClientProvider>,
    );
    const code = page.getByRole("textbox", { name: "Authenticator code" });

    await expect.element(code).toHaveFocus();
    await userEvent.keyboard("123456");

    await expect.element(code).toHaveValue("123456");
    await expect
      .element(code)
      .toHaveAccessibleDescription(
        "Enter the current code from your authenticator app, or an unused backup code.",
      );
  });

  it("reports an attempt the server ended, for the page's end screen", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async () => challengeExpired()),
    );
    const onEnded = vi.fn<(err: ApiError) => void>();
    await render(
      <QueryClientProvider client={new QueryClient()}>
        <CodeForm onDone={vi.fn<() => void>()} onEnded={onEnded} onRestart={vi.fn<() => void>()} />
      </QueryClientProvider>,
    );

    await userEvent.keyboard("123456{Enter}");

    await vi.waitFor(() => expect(onEnded).toHaveBeenCalledTimes(1));
    expect(onEnded.mock.calls[0]?.[0].status).toBe(unauthorized);
  });
});
