import "@/zod-config";
import { StrictMode } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { QueryClientProvider, useMutation } from "@tanstack/react-query";
import { testQueryClient } from "@/test/query-client";
import * as z from "zod";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { CatchBoundary } from "@tanstack/react-router";
import type { ListedAccount } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import { emailAddress } from "@/api/validation";
import { RequestError } from "@/components/request-error";
import { listedAccount } from "@/test/mail-api";
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
        <QueryClientProvider client={testQueryClient()}>
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
type ServerMeta = { action: "test" | "save" };
const saveMeta: ServerMeta = { action: "save" };
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
// password kinds, with two submit buttons; send gets the values and which
// button sent them.
function ServerTestForm({
  send,
}: {
  send: (value: ServerValues, action: string) => Promise<void>;
}) {
  const form = useAppForm({
    defaultValues: serverStart,
    validationLogic: revalidateLogic(),
    validators: { onDynamic: ServerSchema },
    onSubmitMeta: saveMeta,
    onSubmit: ({ value, meta }) => send(value, meta.action),
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
        <form.SubmitButton action="test" working="Saving...">
          Save and test
        </form.SubmitButton>
        <form.SubmitButton action="save" variant="outline" working="Saving...">
          Save only
        </form.SubmitButton>
      </form.Form>
    </form.AppForm>
  );
}

const host = () => page.getByRole("textbox", { name: "Server" });
const saveAndTest = () => page.getByRole("button", { name: "Save and test" });
const saveOnly = () => page.getByRole("button", { name: "Save only" });

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
    const send = vi.fn<(value: ServerValues, action: string) => Promise<void>>(async () => {});
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
    await userEvent.click(saveOnly());

    await vi.waitFor(() =>
      expect(send).toHaveBeenCalledWith(expect.objectContaining({ mode: "none" }), "save"),
    );
  });

  it("move focus to a refused choice's checked radio, which names the error", async () => {
    await render(<ServerTestForm send={async () => {}} />);
    await userEvent.type(host(), "smtp.example.com");
    await userEvent.type(page.getByRole("textbox", { name: "Username" }), "inbok");

    await userEvent.click(page.getByText("None", { exact: true }));
    await userEvent.click(saveOnly());

    const none = page.getByRole("radio", { name: "None" });
    await expect.element(none).toHaveFocus();
    await expect.element(none).toHaveAccessibleDescription("Not encrypted. Choose TLS to sign in.");
  });

  it("send with the pressed button's action, which alone shows it is working", async () => {
    const request = pending();
    const send = vi.fn<(value: ServerValues, action: string) => Promise<void>>(
      () => request.promise,
    );
    await render(<ServerTestForm send={send} />);
    await userEvent.type(host(), "smtp.example.com");

    await userEvent.click(saveOnly());
    const working = page.getByRole("button", { name: "Saving..." });
    await expect.element(working).toHaveAttribute("data-action", "save");
    await expect.element(saveAndTest()).toBeDisabled();
    await expect.element(page.getByRole("radio", { name: "TLS" })).toBeDisabled();
    request.resolve();

    await expect.element(saveAndTest()).toBeEnabled();
    expect(send).toHaveBeenCalledWith(expect.anything(), "save");
  });

  it("send Enter in a field with the first button's action", async () => {
    const send = vi.fn<(value: ServerValues, action: string) => Promise<void>>(async () => {});
    await render(<ServerTestForm send={send} />);

    await userEvent.type(host(), "smtp.example.com{Enter}");

    await vi.waitFor(() => expect(send).toHaveBeenCalledWith(expect.anything(), "test"));
  });
});

// The message editor's rows, as compose and reply use them.
const MessageSchema = z.object({
  connectedAccountId: z.int().min(1, { error: "Choose an account." }),
  to: z.array(z.string()).min(1, { error: "Add a recipient." }),
  body: z.string(),
});
type MessageValues = z.infer<typeof MessageSchema>;
const account: ListedAccount = listedAccount(7, "studio@example.com", 1);
const addressRule = emailAddress(z.string());

// MessageTestForm is a message editor's rows, sending with send.
function MessageTestForm({ send }: { send: (value: MessageValues) => Promise<void> }) {
  const mutation = useMutation<void, ApiError, MessageValues>({ mutationFn: send, gcTime: 0 });
  const request = useFormRequest(mutation, ["connectedAccountId", "to", "body"]);
  const form = useAppForm({
    defaultValues: { connectedAccountId: 0, to: [] as string[], body: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(MessageSchema) },
    onSubmit: async ({ value, formApi }) => {
      await request.send(formApi, value, value);
    },
  });

  return (
    <form.AppForm>
      <form.Form label="Message">
        <form.AppField name="connectedAccountId">
          {(field) => (
            <field.FromField label="From" accounts={[account]} description="About this account." />
          )}
        </form.AppField>
        <form.AppField name="to">
          {(field) => (
            <field.RecipientsField
              label="To"
              isAddress={(value) => addressRule.safeParse(value).success}
            />
          )}
        </form.AppField>
        <form.AppField name="body">
          {(field) => <field.MessageField label="Message" focus="mount" />}
        </form.AppField>
        <form.SubmitButton working="Sending...">Send</form.SubmitButton>
      </form.Form>
    </form.AppForm>
  );
}

describe("message editor rows", () => {
  it("focus the message when asked, and tie each row to its note and error", async () => {
    await render(
      <QueryClientProvider client={testQueryClient()}>
        <MessageTestForm send={async () => {}} />
      </QueryClientProvider>,
    );
    const from = page.getByRole("button", { name: /From/ });
    const to = page.getByRole("textbox", { name: "To" });

    await expect.element(page.getByRole("textbox", { name: "Message" })).toHaveFocus();
    await expect.element(from).toHaveAccessibleDescription("About this account.");

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(from).toHaveFocus();
    await expect.element(to).toHaveAccessibleDescription("Add a recipient.");
    await expect.element(to).toHaveAttribute("aria-invalid", "true");
  });

  it("lock every row while the form sends", async () => {
    const request = pending();
    await render(
      <QueryClientProvider client={testQueryClient()}>
        <MessageTestForm send={() => request.promise} />
      </QueryClientProvider>,
    );
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "studio@example.com" }));
    await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");

    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByRole("button", { name: /From/ })).toBeDisabled();
    await expect.element(page.getByRole("textbox", { name: "To" })).toHaveAttribute("readonly");
    await expect.element(page.getByRole("textbox", { name: "Message" })).toBeDisabled();
    request.resolve();
    await expect.element(page.getByRole("textbox", { name: "Message" })).toBeEnabled();
  });

  it("show long values whole: an account in the open list and a row's error", async () => {
    await render(
      <QueryClientProvider client={testQueryClient()}>
        <MessageTestForm send={async () => {}} />
      </QueryClientProvider>,
    );

    await userEvent.click(page.getByRole("button", { name: /From/ }));
    const address = page.getByRole("option").getByText("studio@example.com").element();
    expect(getComputedStyle(address).whiteSpace).toBe("normal");
    expect(getComputedStyle(address).overflowWrap).toBe("anywhere");
    await userEvent.keyboard("{Escape}");

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    const error = page.getByText("Add a recipient.").element().closest("div");
    expect(error && getComputedStyle(error).overflowWrap).toBe("anywhere");
  });
});

// FolderForm is a form with one select field, Folder, over options that
// nest, sending what was chosen to send.
function FolderForm({ send }: { send: (folder: string) => void }) {
  const form = useAppForm({
    defaultValues: { folder: "top" },
    onSubmit: ({ value }) => send(value.folder),
  });
  return (
    <form.AppForm>
      <form.Form label="Folder test">
        <form.AppField name="folder">
          {(field) => (
            <field.SelectField
              label="Folder"
              description="Where it goes."
              optional
              options={[
                { value: "top", label: "Nothing, at the top" },
                { value: "work", label: "Archive", whole: "Work/Archive", depth: 1 },
                { value: "home", label: "Archive", whole: "Home/Archive", depth: 1 },
              ]}
            />
          )}
        </form.AppField>
        <form.SubmitButton working="Saving...">Save</form.SubmitButton>
      </form.Form>
    </form.AppForm>
  );
}

describe("the select kind", () => {
  it("names each option by its whole name, shows the choice so, and sends its value", async () => {
    const send = vi.fn<(folder: string) => void>();
    await render(<FolderForm send={send} />);

    const folder = page.getByRole("button", { name: /Folder/ });
    await expect.element(folder).toHaveAccessibleDescription("Where it goes.");
    await userEvent.click(folder);
    // Two options share a name in the list; each is heard by its whole one.
    await expect.element(page.getByRole("option", { name: "Work/Archive" })).toBeVisible();
    await userEvent.click(page.getByRole("option", { name: "Home/Archive" }));
    await expect.element(folder).toHaveTextContent("Home/Archive");
    await userEvent.click(page.getByRole("button", { name: "Save" }));

    expect(send).toHaveBeenCalledWith("home");
  });
});
