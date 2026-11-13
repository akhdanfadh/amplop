import "@/zod-config";
import { revalidateLogic } from "@tanstack/react-form";
import * as z from "zod";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { CatchBoundary } from "@tanstack/react-router";
import { ApiError } from "@/api/transport";
import { emailAddress } from "@/api/validation";
import { useAppForm } from "./app-form";
import { useRefusals } from "./refusals";

const unprocessable = 422;
const fields = ["email", "password"] as const;
const Schema = z.object({
  email: emailAddress(z.string().max(254)),
  password: z.string().min(1, { error: "Enter your password." }),
});
type Values = z.infer<typeof Schema>;

// refusal returns the server's 422 refusing the email field with message.
function refusal(message: string, location = "body.email"): ApiError {
  return new ApiError("http", {
    method: "POST",
    status: unprocessable,
    problem: { status: unprocessable, title: "Invalid input", errors: [{ location, message }] },
  });
}

// Send sends a form's values, as a request does.
type Send = (value: Values) => Promise<void>;

// pending returns a request that waits until resolve or reject is called.
function pending(): { promise: Promise<void>; resolve: () => void; reject: (err: Error) => void } {
  const settle: { done?: () => void; fail?: (err: Error) => void } = {};
  const promise = new Promise<void>((done, fail) => {
    settle.done = done;
    settle.fail = fail;
  });
  return { promise, resolve: () => settle.done?.(), reject: (err) => settle.fail?.(err) };
}

// broken fails as a fault in the form's own code, not a request.
function broken(): Promise<void> {
  return Promise.reject(new Error("A bug in the form."));
}

// refusingSecret fails as a refusal of an input the form does not render.
function refusingSecret(): Promise<void> {
  return Promise.reject(refusal("Enter a valid value.", "body.secret"));
}

// TestForm sends its values with send, as a form built on the shared fields
// does: refusals in its check, the request awaited in onSubmit.
function TestForm({ send }: { send: Send }) {
  const refusals = useRefusals<Values>(fields);
  const form = useAppForm({
    defaultValues: { email: "", password: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: refusals.check(Schema) },
    onSubmit: async ({ value, formApi }) => {
      try {
        await send(value);
        refusals.record(formApi, value);
      } catch (err) {
        if (!(err instanceof ApiError)) throw err;
        refusals.record(formApi, value, err);
      }
    },
  });

  return (
    <form.AppForm>
      <form.Form label="Test">
        <form.AppField name="email">
          {(field) => (
            <field.EmailField
              label="Email address"
              description="The one you signed up with."
              autoComplete="username"
            />
          )}
        </form.AppField>
        <form.AppField name="password">
          {(field) => (
            <field.PasswordField label="Password" autoComplete="current-password" optional />
          )}
        </form.AppField>
        <form.SubmitButton working="Sending...">Send</form.SubmitButton>
      </form.Form>
    </form.AppForm>
  );
}

const email = () => page.getByRole("textbox", { name: "Email address" });
const password = () => page.getByLabelText("Password (optional)", { exact: true });
const submit = () => page.getByRole("button", { name: /Send/ });

// formElement returns the rendered <form>, to submit it as Enter in a field
// does.
function formElement(): HTMLFormElement {
  const element = page.getByRole("form", { name: "Test" }).element();
  if (!(element instanceof HTMLFormElement)) throw new Error("No form rendered.");
  return element;
}

describe("shared form fields", () => {
  it("label the control and describe it before any error", async () => {
    await render(<TestForm send={async () => {}} />);

    await expect.element(email()).toHaveAccessibleDescription("The one you signed up with.");
    await expect.element(page.getByText("Password (optional)")).toBeVisible();
    await expect.element(page.getByRole("form", { name: "Test" })).toBeVisible();
  });

  it("show errors only after the first submit, then update them as the member types", async () => {
    await render(<TestForm send={async () => {}} />);

    await userEvent.type(email(), "ana");
    await expect.element(email()).not.toHaveAttribute("aria-invalid", "true");

    await userEvent.click(submit());
    await expect.element(email()).toHaveAttribute("aria-invalid", "true");
    await expect
      .element(email())
      .toHaveAccessibleDescription(
        "The one you signed up with. Enter an email address, like name@example.com.",
      );

    await userEvent.type(email(), "@example.com");
    await expect.element(email()).not.toHaveAttribute("aria-invalid", "true");
  });

  it("move focus to the first invalid field after a submit", async () => {
    await render(<TestForm send={async () => {}} />);

    await userEvent.type(email(), "ana@example.com");
    await userEvent.click(submit());

    await expect.element(password()).toHaveFocus();
  });

  it("show what the button does while sending, and send once", async () => {
    const request = pending();
    const send = vi.fn<Send>(() => request.promise);
    await render(<TestForm send={send} />);
    await userEvent.type(email(), "ana@example.com");
    await userEvent.type(password(), "secret");

    await userEvent.click(submit());
    await expect.element(submit()).toHaveTextContent("Sending...");
    await expect.element(email()).toBeDisabled();
    // A submit that reaches the form itself, which the pending button's
    // own guard never sees.
    formElement().requestSubmit();
    request.resolve();

    await expect.element(submit()).toHaveTextContent("Send");
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("keep focus on the pending button when Enter submits from a field", async () => {
    const request = pending();
    await render(<TestForm send={() => request.promise} />);
    await userEvent.type(email(), "ana@example.com");
    await userEvent.type(password(), "secret");

    await userEvent.keyboard("{Enter}");
    await expect.element(submit()).toHaveTextContent("Sending...");
    await expect.element(submit()).toHaveFocus();
    request.reject(new ApiError("network", { method: "POST" }));

    await expect.element(submit()).toHaveTextContent("Send");
    await expect.element(submit()).toHaveFocus();
  });

  it("keep a shown password from capitals and spelling fixes", async () => {
    await render(<TestForm send={async () => {}} />);

    await userEvent.click(page.getByRole("button", { name: "Show password" }));

    await expect.element(password()).toHaveAttribute("type", "text");
    await expect.element(password()).toHaveAttribute("autocapitalize", "none");
    await expect.element(password()).toHaveAttribute("spellcheck", "false");
  });

  it("show a submit that throws on the crash screen", async () => {
    await render(
      <CatchBoundary getResetKey={() => 0} errorComponent={() => <p>Crashed</p>}>
        <TestForm send={broken} />
      </CatchBoundary>,
    );
    await userEvent.type(email(), "ana@example.com");
    await userEvent.type(password(), "secret");

    await userEvent.click(submit());

    await expect.element(page.getByText("Crashed")).toBeVisible();
  });
});

describe("a server refusal", () => {
  const message = "Enter the email address this link was sent to.";

  // refused renders the form, sends ana@example.com, and leaves the
  // server's refusal of it on the email field.
  async function refused() {
    const send = vi.fn<Send>(() => Promise.reject(refusal(message)));
    await render(<TestForm send={send} />);
    await userEvent.type(email(), "ana@example.com");
    await userEvent.type(password(), "secret");
    await userEvent.click(submit());
    await expect
      .element(email())
      .toHaveAccessibleDescription(`The one you signed up with. ${message}`);
    return send;
  }

  it("shows on its field and takes focus there", async () => {
    await refused();

    await expect.element(email()).toHaveFocus();
  });

  it("stays when the member leaves the field or edits another", async () => {
    await refused();

    await userEvent.type(password(), "2");

    await expect.element(email()).toHaveAttribute("aria-invalid", "true");
  });

  it("clears when the field changes and returns with the refused value", async () => {
    await refused();

    await userEvent.type(email(), "m");
    await expect.element(email()).not.toHaveAttribute("aria-invalid", "true");

    await userEvent.keyboard("{Backspace}");
    await expect.element(email()).toHaveAttribute("aria-invalid", "true");
  });

  it("keeps the refused value from being sent again", async () => {
    const send = await refused();

    await userEvent.click(submit());

    await expect.element(email()).toHaveFocus();
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("marks no field for an input the form does not render", async () => {
    await render(<TestForm send={refusingSecret} />);
    await userEvent.type(email(), "ana@example.com");
    await userEvent.type(password(), "secret");

    await userEvent.click(submit());

    await expect.element(submit()).toHaveFocus();
    await expect.element(email()).not.toHaveAttribute("aria-invalid", "true");
    await expect.element(password()).not.toHaveAttribute("aria-invalid", "true");
  });
});
