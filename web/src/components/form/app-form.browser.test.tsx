import "@/zod-config";
import { StrictMode } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { QueryClient, QueryClientProvider, useMutation } from "@tanstack/react-query";
import * as z from "zod";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { CatchBoundary } from "@tanstack/react-router";
import { ApiError } from "@/api/transport";
import { emailAddress } from "@/api/validation";
import { RequestError } from "@/components/request-error";
import { useAppForm } from "./app-form";
import { useFormRequest } from "./request";

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

// TestForm sends its values with send through a mutation, as a form built
// on the shared fields does, and calls onDone once an answer comes.
function TestForm({ send, onDone }: { send: Send; onDone: () => void }) {
  const mutation = useMutation<void, ApiError, Values>({ mutationFn: send, gcTime: 0 });
  const request = useFormRequest(mutation, fields);
  const form = useAppForm({
    defaultValues: { email: "", password: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(Schema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, value);
      if (sent.outcome === "done") onDone();
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
        <RequestError errors={request.summary(() => "The request failed.")} />
      </form.Form>
    </form.AppForm>
  );
}

// show renders TestForm sending with send in Strict Mode, as the app runs
// in development, under a crash screen. It returns onDone and the screen.
async function show(send: Send) {
  const onDone = vi.fn<() => void>();
  const screen = await render(
    <StrictMode>
      <CatchBoundary getResetKey={() => 0} errorComponent={() => <p>Crashed</p>}>
        <QueryClientProvider client={new QueryClient()}>
          <TestForm send={send} onDone={onDone} />
        </QueryClientProvider>
      </CatchBoundary>
    </StrictMode>,
  );
  return { onDone, screen };
}

// fill types a valid email address and password.
async function fill() {
  await userEvent.type(email(), "ana@example.com");
  await userEvent.type(password(), "secret");
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
    await show(async () => {});

    await expect.element(email()).toHaveAccessibleDescription("The one you signed up with.");
    await expect.element(page.getByText("Password (optional)")).toBeVisible();
    await expect.element(page.getByRole("form", { name: "Test" })).toBeVisible();
  });

  it("show errors only after the first submit, then update them as the member types", async () => {
    await show(async () => {});

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
    await show(async () => {});

    await userEvent.type(email(), "ana@example.com");
    await userEvent.click(submit());

    await expect.element(password()).toHaveFocus();
  });

  it("show what the button does while sending, and send once", async () => {
    const request = pending();
    const send = vi.fn<Send>(() => request.promise);
    await show(send);
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
    await show(() => request.promise);
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
    await show(async () => {});

    await userEvent.click(page.getByRole("button", { name: "Show password" }));

    await expect.element(password()).toHaveAttribute("type", "text");
    await expect.element(password()).toHaveAttribute("autocapitalize", "none");
    await expect.element(password()).toHaveAttribute("spellcheck", "false");
  });

  it("show a submit that throws on the crash screen", async () => {
    await show(broken);
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
    await show(send);
    await userEvent.type(email(), "ana@example.com");
    await userEvent.type(password(), "secret");
    await userEvent.click(submit());
    await expect
      .element(email())
      .toHaveAccessibleDescription(`The one you signed up with. ${message}`);
    return send;
  }

  it("shows on its field and takes focus there, with no summary", async () => {
    await refused();

    await expect.element(email()).toHaveFocus();
    await expect.element(page.getByRole("alert")).not.toBeInTheDocument();
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

  it("of an input the form does not render shows in the summary, on no field", async () => {
    await show(refusingSecret);
    await fill();

    await userEvent.click(submit());

    await expect
      .element(page.getByRole("alert"))
      .toHaveTextContent("The request failed.Enter a valid value.");
    await expect.element(submit()).toHaveFocus();
    await expect.element(email()).not.toHaveAttribute("aria-invalid", "true");
    await expect.element(password()).not.toHaveAttribute("aria-invalid", "true");
  });
});

describe("a form request", () => {
  it("reports an answer once", async () => {
    const { onDone } = await show(async () => {});
    await fill();

    await userEvent.click(submit());

    await vi.waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
  });

  it("ignores an answer that comes after its form is gone", async () => {
    const request = pending();
    const send = vi.fn<Send>(() => request.promise);
    const { onDone, screen } = await show(send);
    await fill();
    await userEvent.click(submit());
    await vi.waitFor(() => expect(send).toHaveBeenCalled());

    // The member follows a link away while the request runs.
    await screen.unmount();
    request.resolve();
    // The awaited code resumes a task after the request settles.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(onDone).not.toHaveBeenCalled();
  });
});

const CodeSchema = z.object({ code: z.string().length(6, { error: "Enter all 6 digits." }) });

// CodeTestForm is a form with one code field, six digits, with an action
// beside its label.
function CodeTestForm() {
  const form = useAppForm({
    defaultValues: { code: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: CodeSchema },
  });

  return (
    <form.AppForm>
      <form.Form label="Code test">
        <form.AppField name="code">
          {(field) => (
            <field.CodeField
              label="Authenticator code"
              description="The six digits your app shows."
              length={6}
              pattern="^\d+$"
              inputMode="numeric"
              paste={(text) => text.replace(/\s/g, "")}
              labelAction={<button type="button">Use a backup code</button>}
            />
          )}
        </form.AppField>
        <form.SubmitButton working="Checking...">Check</form.SubmitButton>
      </form.Form>
    </form.AppForm>
  );
}

const code = () => page.getByRole("textbox", { name: "Authenticator code" });

// top returns where element's box starts, in CSS pixels from the viewport.
function top(element: Element): number {
  return element.getBoundingClientRect().top;
}

describe("a code field", () => {
  it("is named by its label, with its action beside it and its description read", async () => {
    await render(<CodeTestForm />);

    await expect.element(code()).toHaveAccessibleDescription("The six digits your app shows.");
    await expect.element(code()).toHaveAttribute("inputmode", "numeric");
    await expect.element(page.getByRole("button", { name: "Use a backup code" })).toBeVisible();
  });

  it("shows its check's error after a submit", async () => {
    await render(<CodeTestForm />);

    await userEvent.click(page.getByRole("button", { name: "Check" }));

    await expect.element(code()).toHaveAttribute("aria-invalid", "true");
    await expect
      .element(code())
      .toHaveAccessibleDescription("The six digits your app shows. Enter all 6 digits.");
  });

  it("takes a pasted code through paste, e.g. without its spaces", async () => {
    await render(<CodeTestForm />);
    const input = code().element();
    if (!(input instanceof HTMLInputElement)) throw new Error("No code input rendered.");
    input.focus();

    const data = new DataTransfer();
    data.setData("text/plain", "123 456");
    input.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true }));

    await expect.element(code()).toHaveValue("123456");
  });

  it("puts its action on the label's line but after the field in Tab order", async () => {
    await render(<CodeTestForm />);
    const action = page.getByRole("button", { name: "Use a backup code" });

    await userEvent.click(code());
    await userEvent.tab();

    await expect.element(action).toHaveFocus();
    const label = page.getByText("Authenticator code", { exact: true }).element();
    expect(Math.abs(top(action.element()) - top(label))).toBeLessThan(8);
  });
});

const ServerSchema = z
  .object({
    host: z.string().regex(/\S/),
    port: z.string(),
    mode: z.enum(["tls", "none"]),
    username: z.string(),
    password: z.string(),
  })
  .refine((value) => value.mode !== "none" || !value.username, {
    path: ["mode"],
    error: "Choose TLS to sign in.",
  });
type ServerValues = z.infer<typeof ServerSchema>;
const serverStart: ServerValues = {
  host: "",
  port: "465",
  mode: "tls",
  username: "",
  password: "",
};

const modes = [
  { value: "tls", label: "TLS", description: "Encrypted." },
  { value: "none", label: "None", description: "Not encrypted." },
] as const;

// ServerTestForm holds another service's settings in the text, choice, and
// password kinds; send gets the values.
function ServerTestForm({ send }: { send: (value: ServerValues) => Promise<void> }) {
  const form = useAppForm({
    defaultValues: serverStart,
    validationLogic: revalidateLogic(),
    validators: { onDynamic: ServerSchema },
    onSubmit: ({ value }) => send(value),
  });

  return (
    <form.AppForm>
      <form.Form label="Server test">
        <form.AppField name="host">
          {(field) => (
            <field.TextField
              label="Server"
              description="The host name."
              autoComplete="off"
              text="literal"
            />
          )}
        </form.AppField>
        <form.AppField name="mode">
          {(field) => <field.ChoiceField label="Security" options={modes} />}
        </form.AppField>
        <form.AppField name="port">
          {(field) => (
            <field.TextField label="Port" autoComplete="off" inputMode="numeric" width="short" />
          )}
        </form.AppField>
        <form.AppField name="username">
          {(field) => (
            <field.TextField
              label="Username"
              autoComplete="off"
              text="literal"
              passwordManagers="ignore"
            />
          )}
        </form.AppField>
        <form.AppField name="password">
          {(field) => (
            <field.PasswordField label="Password" autoComplete="off" passwordManagers="ignore" />
          )}
        </form.AppField>
        <form.SubmitButton working="Saving...">Save</form.SubmitButton>
      </form.Form>
    </form.AppForm>
  );
}

const host = () => page.getByRole("textbox", { name: "Server" });
const save = () => page.getByRole("button", { name: "Save" });

describe("the text, choice, and password kinds", () => {
  it("type a literal value without capitals or spelling fixes, and a port on a number pad", async () => {
    await render(<ServerTestForm send={async () => {}} />);

    await expect.element(host()).toHaveAttribute("autocapitalize", "none");
    await expect.element(host()).toHaveAttribute("spellcheck", "false");
    await expect.element(host()).toHaveAccessibleDescription("The host name.");
    const port = page.getByRole("textbox", { name: "Port" });
    await expect.element(port).toHaveAttribute("inputmode", "numeric");
    await expect.element(port).toHaveAttribute("type", "text");
  });

  it("keep password managers out of another service's credentials", async () => {
    await render(<ServerTestForm send={async () => {}} />);

    const username = page.getByRole("textbox", { name: "Username" });
    const serverPassword = page.getByLabelText("Password", { exact: true });
    await expect.element(username).toHaveAttribute("data-1p-ignore");
    await expect.element(username).toHaveAttribute("data-lpignore", "true");
    await expect.element(username).toHaveAttribute("data-bwignore");
    await expect.element(username).toHaveAttribute("data-form-type", "other");
    await expect.element(serverPassword).toHaveAttribute("data-1p-ignore");
    await expect.element(serverPassword).toHaveAttribute("data-lpignore", "true");
    await expect.element(serverPassword).toHaveAttribute("data-bwignore");
    await expect.element(serverPassword).toHaveAttribute("data-form-type", "other");
    // Browsers ignore "off" on a password; "new-password" keeps them from
    // filling a saved one.
    await expect
      .element(page.getByLabelText("Password", { exact: true }))
      .toHaveAttribute("autocomplete", "new-password");
  });

  it("choose an option by keyboard, under the label as its group's name", async () => {
    const send = vi.fn<(value: ServerValues) => Promise<void>>(async () => {});
    await render(<ServerTestForm send={send} />);
    await userEvent.type(host(), "smtp.example.com");

    await expect.element(page.getByRole("radiogroup", { name: "Security" })).toBeVisible();
    await expect
      .element(page.getByRole("radio", { name: "TLS" }))
      .toHaveAccessibleDescription("Encrypted.");
    // The radio itself sits under its drawn dot; its label takes the click.
    await userEvent.click(page.getByText("TLS", { exact: true }));
    await userEvent.keyboard("{ArrowDown}");
    await expect.element(page.getByRole("radio", { name: "None" })).toBeChecked();
    // The line about an option is part of its row: pressing it chooses it.
    await userEvent.click(page.getByText("Encrypted.", { exact: true }));
    await expect.element(page.getByRole("radio", { name: "TLS" })).toBeChecked();
    await userEvent.keyboard("{ArrowDown}");
    await userEvent.click(save());

    await vi.waitFor(() =>
      expect(send).toHaveBeenCalledWith(expect.objectContaining({ mode: "none" })),
    );
  });

  it("move focus to a refused choice's checked radio, which names the error", async () => {
    await render(<ServerTestForm send={async () => {}} />);
    await userEvent.type(host(), "smtp.example.com");
    await userEvent.type(page.getByRole("textbox", { name: "Username" }), "inbok");

    await userEvent.click(page.getByText("None", { exact: true }));
    await userEvent.click(save());

    const none = page.getByRole("radio", { name: "None" });
    await expect.element(none).toHaveFocus();
    await expect.element(none).toHaveAccessibleDescription("Not encrypted. Choose TLS to sign in.");
  });
});
