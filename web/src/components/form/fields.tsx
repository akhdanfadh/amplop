import { Fragment, useEffect, useId, useRef, type ReactElement, type ReactNode } from "react";
import { useSelector } from "@tanstack/react-form";
import { cn } from "@/lib/cn";
import { CircleAlertIcon } from "lucide-react";
import type { ConnectedAccount } from "@/api/generated/model";
import { accountDot } from "@/components/account-mark";
import { PasswordInput } from "@/components/password-input";
import { RecipientInput, type Typing } from "@/components/recipient-input";
import { Field, FieldDescription, FieldError, FieldLabel, FieldTitle } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSeparator,
  InputOTPSlot,
} from "@/components/ui/input-otp";
import { useFieldContext, useFormContext } from "./form-context";

// What every shared field takes: its label, a description shown before the
// control, and whether the field may be left empty, which the label then
// says. labelAction sits at the end of the label's line, e.g. a "Forgot
// password?" link, and after the control in Tab order.
type FieldProps = {
  label: string;
  description?: string;
  optional?: boolean;
  labelAction?: ReactNode;
};

// Whose credentials a field holds: "ignore" keeps password managers out of
// a field holding another service's, e.g. a mail server's username and
// password, so they neither fill in the member's inbok sign-in nor offer
// to save the field as one.
type PasswordManagers = "ignore";

// Password managers each read their own attribute to leave a field alone
// (1Password, LastPass, Bitwarden, Dashlane, in that order). Browsers'
// own managers have none: they may still offer to save what is sent.
const ignoredByManagers = {
  "data-1p-ignore": true,
  "data-lpignore": "true",
  "data-bwignore": true,
  "data-form-type": "other",
};

// The props of the current field's control: they tie it to its label,
// description, and error, carry its value, and lock it while the form sends.
type ControlProps = {
  id: string;
  name: string;
  value: string;
  disabled: boolean;
  "aria-invalid": boolean;
  "aria-describedby": string | undefined;
  onBlur: () => void;
};

// TextField is the current field as a one-line text input. autoComplete
// tells the browser what it holds; "off" is for a value that is not the
// member's own, e.g. a mail server's host, and other values join with the
// first field that needs one. text "literal" is typed exactly, e.g. a host
// name, so a phone neither capitalises nor corrects it. inputMode picks a phone's keyboard,
// e.g. "numeric" for a number that names something, like a port, which
// arrow keys must not change as they do in type "number" (GOV.UK Design
// System, "Asking for numbers"). width "short" fits a few characters.
export function TextField({
  autoComplete,
  text = "prose",
  inputMode,
  width = "full",
  passwordManagers,
  ...frame
}: FieldProps & {
  autoComplete: "off";
  text?: "prose" | "literal";
  inputMode?: "numeric";
  width?: "full" | "short";
  passwordManagers?: PasswordManagers;
}): ReactElement {
  return (
    <FieldFrame {...frame}>
      {(control, change) => (
        <Input
          {...control}
          {...(passwordManagers === "ignore" && ignoredByManagers)}
          onChange={(event) => change(event.target.value)}
          type="text"
          autoComplete={autoComplete}
          inputMode={inputMode}
          autoCapitalize={text === "literal" ? "none" : undefined}
          spellCheck={text === "literal" ? false : undefined}
          className={cn(width === "short" && "max-w-32")}
        />
      )}
    </FieldFrame>
  );
}

// EmailField is the current field as an email address input. autoComplete
// tells the browser which address it holds: "username" for one that signs
// in to inbok, so a password manager saves it with the password, "email" for
// another of the member's own, "off" for anyone else's.
export function EmailField({
  autoComplete,
  ...text
}: FieldProps & { autoComplete: "username" | "email" | "off" }): ReactElement {
  return (
    <FieldFrame {...text}>
      {(control, change) => (
        <Input
          {...control}
          onChange={(event) => change(event.target.value)}
          // The browser also strips spaces around the value of type "email"
          // (HTML's value sanitization), as the server does.
          type="email"
          autoComplete={autoComplete}
          autoCapitalize="none"
          spellCheck={false}
        />
      )}
    </FieldFrame>
  );
}

// PasswordField is the current field as a password input with a button that
// shows it. autoComplete is "current-password" to sign in, "new-password" to
// choose one, or "off" for a password that is not the member's own, e.g. a
// mail server's. toggle names the password in the button's label, e.g.
// "Show new password confirmation"; it defaults to the label in lowercase.
// passwordManagers "ignore" marks another service's password: it also sets
// autoComplete to "new-password", since browsers ignore "off" on a
// password (MDN, "autocomplete": preventing autofilling).
export function PasswordField({
  autoComplete,
  toggle,
  passwordManagers,
  ...text
}: FieldProps & {
  autoComplete: "current-password" | "new-password" | "off";
  toggle?: string;
  passwordManagers?: PasswordManagers;
}): ReactElement {
  const ignored = passwordManagers === "ignore";
  return (
    <FieldFrame {...text}>
      {(control, change) => (
        <PasswordInput
          {...control}
          {...(ignored && ignoredByManagers)}
          onChange={(event) => change(event.target.value)}
          label={toggle ?? text.label.toLowerCase()}
          type="password"
          autoComplete={ignored ? "new-password" : autoComplete}
          // Shown, the password is a text input, where a phone's keyboard
          // may capitalise or correct it (GOV.UK Design System, "Password
          // input": do not spellcheck or autocapitalise).
          autoCapitalize="none"
          spellCheck={false}
        />
      )}
    </FieldFrame>
  );
}

// ChoiceField is the current field as one choice among options, each a
// radio with its label and a line about it, under the field's label, which
// names the group. Its value is the chosen option's value.
export function ChoiceField({
  label,
  description,
  options,
}: Omit<FieldProps, "optional" | "labelAction"> & {
  options: readonly { value: string; label: string; description: string }[];
}): ReactElement {
  const field = useFieldContext<string>();
  const form = useFormContext();
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  const id = useId();
  const errors = messages(field.state.meta.errors);
  const invalid = errors.length > 0;
  const errorId = `${id}-error`;

  return (
    // Not a fieldset: the radio group carries the name, and a fieldset
    // around it would announce it twice.
    <div className="flex flex-col gap-2">
      <FieldTitle id={`${id}-label`} className={cn(invalid && "text-destructive")}>
        {label}
      </FieldTitle>
      {description && <FieldDescription id={`${id}-description`}>{description}</FieldDescription>}
      <RadioGroup
        name={field.name}
        value={field.state.value}
        isDisabled={sending}
        isInvalid={invalid}
        aria-labelledby={`${id}-label`}
        aria-describedby={description ? `${id}-description` : undefined}
        onChange={(value) => {
          // React Aria types the value as a string; keep only an offered one.
          const option = options.find((offered) => offered.value === value);
          if (option) field.handleChange(option.value);
        }}
      >
        {options.map((option) => (
          <RadioGroupItem
            key={option.value}
            value={option.value}
            // The line about the option sits in its row, so a press on it
            // chooses it, but only the label names it.
            aria-labelledby={`${id}-${option.value}-label`}
            // Focus lands on a radio, not the group, so each names the error.
            aria-describedby={[`${id}-${option.value}`, invalid && errorId]
              .filter(Boolean)
              .join(" ")}
            onBlur={field.handleBlur}
            className="items-start py-1 *:data-[slot=radio-group-indicator]:mt-0.5"
          >
            <span className="flex flex-col gap-0.5">
              <span id={`${id}-${option.value}-label`}>{option.label}</span>
              <span id={`${id}-${option.value}`} className="text-muted-foreground">
                {option.description}
              </span>
            </span>
          </RadioGroupItem>
        ))}
      </RadioGroup>
      {invalid && <ErrorLine id={errorId} errors={errors} />}
    </div>
  );
}

// CodeField is the current field as a one-time code of length characters
// in two halves, one box per character, e.g. an authenticator's six digits.
// pattern is the characters it takes, e.g. input-otp's REGEXP_ONLY_DIGITS,
// inputMode picks a phone's keyboard, e.g. "numeric" for the number pad,
// and paste turns pasted text into the code, e.g. dropping its spaces.
export function CodeField({
  length,
  pattern,
  inputMode,
  paste,
  ...text
}: FieldProps & {
  length: number;
  pattern: string;
  inputMode: "numeric" | "text";
  paste?: (text: string) => string;
}): ReactElement {
  const half = Math.ceil(length / 2);

  return (
    <FieldFrame {...text}>
      {(control, change) => (
        <InputOTP
          {...control}
          onChange={change}
          maxLength={length}
          pattern={pattern}
          inputMode={inputMode}
          pasteTransformer={paste}
          // Lets the browser fill a code an SMS or a password manager holds
          // (HTML's autocomplete "one-time-code").
          autoComplete="one-time-code"
          autoCapitalize="none"
          spellCheck={false}
          containerClassName="max-w-full flex-wrap gap-2"
        >
          {[0, half].map((start) => (
            <Fragment key={start}>
              <InputOTPGroup className="min-w-0 flex-1 basis-24">
                {Array.from({ length: Math.min(half, length - start) }, (_, offset) => (
                  <InputOTPSlot
                    key={start + offset}
                    index={start + offset}
                    // The input, not a box, says the field is invalid; a box
                    // only takes its colour.
                    className="group-data-[invalid=true]/field:border-destructive"
                  />
                ))}
              </InputOTPGroup>
              {start === 0 && <InputOTPSeparator />}
            </Fragment>
          ))}
        </InputOTP>
      )}
    </FieldFrame>
  );
}

// The message editor's rows (DESIGN.md "Compose and reply"): one card with
// a row each for From, the recipient lists, and the subject, a muted label
// on the left and the value on the right, divided by hairlines, then the
// message. The label column is as wide as "Subject", the longest label.
// Each row is a size container: narrower than 24rem, e.g. on a phone or
// with text at 200%, the label sits above the value, which then has the
// row's whole width.
const rowLabel =
  "h-8 w-14 shrink-0 font-normal text-muted-foreground pointer-coarse:h-9 @max-[24rem]:h-6 @max-[24rem]:w-auto @max-[24rem]:flex-1";
// A row's line: the label, the value, and anything after it.
const rowLine = "flex items-start gap-3 px-4 py-1.5 @max-[24rem]:flex-wrap @max-[24rem]:gap-y-0";
// The value of a row whose label sits above it.
const narrowValue = "@max-[24rem]:order-last @max-[24rem]:basis-full";
// Lines under a row's value, e.g. its error, start where the value does:
// the row's padding, the label, and the gap.
const underValue = "px-4 pb-2 pl-21 @max-[24rem]:pl-4";

// An account's address or name in From: cut short in the button, whole in
// the list.
const accountText =
  "min-w-0 truncate in-data-[slot=select-item]:whitespace-normal in-data-[slot=select-item]:wrap-anywhere";

// What an account option that can't send says after its address.
const needsReconnectMark = "(needs reconnecting)";

// FromField is the current field as the message editor's From row: the
// account a message goes from, by its id, 0 while none is chosen. accounts
// are the member's connected accounts; one the server doesn't report as
// usable needs reconnecting and can't be chosen. description says
// something about the choice under the row, e.g. that the chosen account
// didn't receive the message answered.
export function FromField({
  label,
  accounts,
  description,
}: {
  label: string;
  accounts: readonly ConnectedAccount[];
  description?: string;
}): ReactElement {
  return (
    <RowFrame<number> description={description}>
      {(control, change) => (
        <Select
          id={control.id}
          name={control.name}
          className="flex w-full items-center gap-3 px-4 py-1.5 @max-[24rem]:flex-wrap @max-[24rem]:gap-y-0"
          placeholder="Choose an account"
          value={control.value === 0 ? null : control.value}
          onChange={(value) => change(value === null ? 0 : Number(value))}
          // Its send could only fail; the server refuses it too.
          disabledKeys={accounts
            .filter((account) => account.status !== "usable")
            .map((account) => account.id)}
          isDisabled={control.disabled}
          isInvalid={control["aria-invalid"]}
          aria-describedby={control["aria-describedby"]}
          onBlur={control.onBlur}
        >
          {/* Narrow, the label takes its own line and the button keeps its
              width, its text in line with the label. */}
          <Label className={cn(rowLabel, "flex items-center @max-[24rem]:basis-full")}>
            {label}
          </Label>
          {/* Quiet until pointed at: no outline at rest, a hover tint, then
              the list; its chevron and label mark it. */}
          <SelectTrigger
            size="sm"
            className={cn(
              "w-auto max-w-full min-w-0 border-transparent bg-transparent px-2 hover:bg-accent data-pressed:bg-accent dark:bg-transparent dark:hover:bg-accent",
              "@max-[24rem]:-ml-2",
              // 14 px at every width, as the labels and chips are. Inputs
              // keep 16 px on phones so Safari doesn't zoom in on focus;
              // a button never zooms.
              "text-sm",
            )}
          >
            <SelectValue className="min-w-0" />
          </SelectTrigger>
          <SelectContent className="min-w-64">
            {accounts.map((account) => (
              <SelectItem
                key={account.id}
                id={account.id}
                // Typing an option's start selects it, so its words are
                // what the member sees: address, then name.
                textValue={
                  account.name === null ? account.email : `${account.email} ${account.name}`
                }
              >
                {/* The account's mark, as everywhere it is named (DESIGN.md
                    "Colour"), then its address, which recipients see, and
                    its name, which only the member does. */}
                <span
                  aria-hidden="true"
                  className={cn(
                    "size-2 shrink-0 self-center rounded-full",
                    accountDot(account.color),
                  )}
                />
                {/* In the button both shrink and end with "..." when the row
                    is too narrow; in the open list they wrap, so every
                    account reads in full. */}
                <span translate="no" className={accountText}>
                  {account.email}
                </span>
                {account.name !== null && (
                  <span className={cn(accountText, "text-muted-foreground")}>{account.name}</span>
                )}
                {account.status !== "usable" && ` ${needsReconnectMark}`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
    </RowFrame>
  );
}

// RecipientsField is the current field as a message editor's row of
// recipient chips, e.g. To. isAddress is the contract's rule for one
// address, which marks a chip that isn't one. after sits at the row's end,
// e.g. the buttons that add Cc and Bcc. onTyping reports whether the row
// holds typed text not yet a chip, which a form counts as an edit.
export function RecipientsField({
  label,
  isAddress,
  after,
  onTyping,
}: {
  label: string;
  isAddress: (value: string) => boolean;
  after?: ReactNode;
  onTyping?: (state: Typing) => void;
}): ReactElement {
  return (
    <RowFrame<string[]>>
      {(control, change) => (
        <div className={rowLine}>
          <Label htmlFor={control.id} className={rowLabel}>
            {label}
          </Label>
          <RecipientInput
            id={control.id}
            label={label}
            isAddress={isAddress}
            value={control.value}
            onChange={change}
            onBlur={control.onBlur}
            invalid={control["aria-invalid"]}
            describedBy={control["aria-describedby"]}
            // Read-only, not disabled: the chips keep their one Tab stop.
            readOnly={control.disabled}
            onTyping={onTyping}
            className={cn("min-h-8 pointer-coarse:min-h-9", narrowValue)}
          />
          {after}
        </div>
      )}
    </RowFrame>
  );
}

// SubjectField is the current field as a message editor's one-line text
// row, e.g. Subject, without a box of its own.
export function SubjectField({ label }: { label: string }): ReactElement {
  return (
    <RowFrame<string>>
      {(control, change) => (
        <div className={rowLine}>
          <Label htmlFor={control.id} className={cn(rowLabel, "flex items-center")}>
            {label}
          </Label>
          <input
            {...control}
            onChange={(event) => change(event.target.value)}
            type="text"
            autoComplete="off"
            className={cn(
              "h-8 min-w-0 flex-1 bg-transparent px-1 text-base outline-none md:text-sm pointer-coarse:h-9",
              narrowValue,
            )}
          />
        </div>
      )}
    </RowFrame>
  );
}

// MessageField is the current field as a message editor's message: a text
// area without a box of its own, named label for assistive technology.
// focus "mount" moves focus into it when it appears, e.g. a reply's, whose
// recipients are already filled in.
export function MessageField({ label, focus }: { label: string; focus?: "mount" }): ReactElement {
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (focus === "mount") ref.current?.focus();
  }, [focus]);

  return (
    <RowFrame<string> divider="none">
      {(control, change) => (
        <textarea
          {...control}
          ref={ref}
          aria-label={label}
          onChange={(event) => change(event.target.value)}
          // It grows with its text up to 60% of the window, then scrolls.
          className="block field-sizing-content max-h-[60svh] min-h-44 w-full resize-y bg-transparent px-4 py-3 text-message outline-none"
        />
      )}
    </RowFrame>
  );
}

// RowFrame renders the current field as one row of the message editor: the
// row children returns from the field's control props and a function that
// sets its value, then description and the field's errors under the value.
// divider "none" leaves out the hairline under the row, e.g. for the
// message, which the editor's bar edges.
function RowFrame<T>({
  description,
  divider = "below",
  children,
}: {
  description?: string;
  divider?: "below" | "none";
  children: (control: RowControlProps<T>, change: (value: T) => void) => ReactNode;
}): ReactElement {
  const field = useFieldContext<T>();
  const form = useFormContext();
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  const id = useId();
  const descriptionId = `${id}-description`;
  const errorId = `${id}-error`;
  const errors = messages(field.state.meta.errors);
  const invalid = errors.length > 0;
  const describedBy = [description && descriptionId, invalid && errorId].filter(Boolean).join(" ");

  return (
    <div data-invalid={invalid} className={cn("@container", divider === "below" && "border-b")}>
      {children(
        {
          id,
          name: field.name,
          value: field.state.value,
          disabled: sending,
          "aria-invalid": invalid,
          "aria-describedby": describedBy || undefined,
          onBlur: field.handleBlur,
        },
        field.handleChange,
      )}
      {description && (
        // An address in the note, e.g. one that needs reconnecting, breaks
        // anywhere rather than run past the card.
        <p
          id={descriptionId}
          className={cn(underValue, "text-snippet wrap-anywhere text-muted-foreground")}
        >
          {description}
        </p>
      )}
      {invalid && (
        // An error naming a long value, e.g. an address, breaks anywhere
        // rather than run past the card.
        <div className={cn(underValue, "min-w-0 wrap-anywhere")}>
          <ErrorLine id={errorId} errors={errors} />
        </div>
      )}
    </div>
  );
}

// The props of a message editor row's control, as ControlProps with a
// value of the field's own type.
type RowControlProps<T> = Omit<ControlProps, "value"> & { value: T };

// FieldFrame renders the current field's label, description, the control
// children returns from its props and a function that sets the field's
// value, and the field's errors under it. Every stacked field kind renders
// through it; a message editor's rows render through RowFrame.
function FieldFrame({
  label,
  description,
  optional,
  labelAction,
  children,
}: FieldProps & {
  children: (control: ControlProps, change: (value: string) => void) => ReactNode;
}): ReactElement {
  const field = useFieldContext<string>();
  const form = useFormContext();
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  const id = useId();
  const descriptionId = `${id}-description`;
  const errorId = `${id}-error`;
  const errors = messages(field.state.meta.errors);
  const invalid = errors.length > 0;
  const describedBy = [description && descriptionId, invalid && errorId].filter(Boolean).join(" ");
  const fieldLabel = (
    <FieldLabel htmlFor={id}>
      {label}
      {optional && <span className="font-normal text-muted-foreground">{" (optional)"}</span>}
    </FieldLabel>
  );

  return (
    <Field
      data-invalid={invalid}
      data-disabled={sending}
      // With an action, a two-column grid puts it at the end of the label's
      // line while it comes after the control in the page, so Tab goes
      // field, then action: typing an email, Tab, then a password reaches
      // the password, not "Forgot password?" (WCAG 2.4.3 Focus Order).
      className={cn(
        labelAction &&
          "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 [&>:not([data-label-row])]:col-span-2",
      )}
    >
      {labelAction ? (
        <div data-label-row className="col-start-1 row-start-1">
          {fieldLabel}
        </div>
      ) : (
        fieldLabel
      )}
      {description && <FieldDescription id={descriptionId}>{description}</FieldDescription>}
      {children(
        {
          id,
          name: field.name,
          value: field.state.value,
          disabled: sending,
          "aria-invalid": invalid,
          "aria-describedby": describedBy || undefined,
          onBlur: field.handleBlur,
        },
        field.handleChange,
      )}
      {labelAction && (
        <div data-label-row className="col-start-2 row-start-1 justify-self-end">
          {labelAction}
        </div>
      )}
      {invalid && <ErrorLine id={errorId} errors={errors} />}
    </Field>
  );
}

// ErrorLine shows a field's errors under its control, with an icon, so the
// error does not rely on colour alone. id is what the control's
// aria-describedby names.
function ErrorLine({ id, errors }: { id: string; errors: { message: string }[] }): ReactElement {
  return (
    <div className="flex gap-1.5 text-destructive">
      <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <FieldError id={id} errors={errors} />
    </div>
  );
}

// messages returns a field's errors as messages: Zod's issues and the
// server's refusals carry one; a check returning text gives it whole.
function messages(errors: readonly unknown[]): { message: string }[] {
  return errors.flatMap((error) => {
    if (typeof error === "string") return [{ message: error }];
    if (typeof error === "object" && error !== null && "message" in error) {
      if (typeof error.message === "string") return [{ message: error.message }];
    }
    return [];
  });
}
