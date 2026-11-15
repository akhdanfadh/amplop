import { useRef, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { getListConnectedAccountsQueryKey, useRenameConnectedAccount } from "@/api/generated/inbok";
import type { ListedAccount, ResponseConnectedAccounts } from "@/api/generated/model";
import {
  RenameConnectedAccountBody,
  renameConnectedAccountBodyNameMax,
} from "@/api/generated/model/renameConnectedAccountBody.zod";
import { formErrors, formSummary, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { LeaveWarning } from "@/components/leave-warning";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { nameLabel, nameLeaveWarning, savedText, saveNameText, savingNameText } from "./text";

const unauthorized = 401;
const nameFields = ["name"] as const;

// AccountNameForm sets what the member calls account, shown in mail
// instead of its address (DESIGN.md "Sidebar"); an empty name goes back to
// the address. It updates the accounts list with the server's answer.
// describedBy names the element that explains the field, which the page
// shows once for every account's; onUnauthorized runs on a 401.
export function AccountNameForm({
  account,
  describedBy,
  onUnauthorized,
}: {
  account: ListedAccount;
  describedBy?: string;
  onUnauthorized: () => void;
}): ReactElement {
  const client = useQueryClient();
  const formRef = useRef<HTMLFormElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  const id = `account-name-${account.id}`;
  const rename = useRenameConnectedAccount({
    mutation: {
      gcTime: 0,
      onSuccess: async (result) => {
        const renamed = result.data.account;
        // A list read already under way holds the old name and would put
        // it back as it lands; the next read has the new one.
        await client.cancelQueries({ queryKey: getListConnectedAccountsQueryKey(), exact: true });
        client.setQueryData<ResponseConnectedAccounts>(getListConnectedAccountsQueryKey(), (list) =>
          list
            ? {
                ...list,
                data: {
                  ...list.data,
                  accounts: list.data.accounts.map((a) =>
                    a.id === renamed.id ? { ...a, name: renamed.name } : a,
                  ),
                },
              }
            : list,
        );
        // The server drops the white space around a name, so the field
        // shows what was kept.
        form.reset({ name: renamed.name ?? "" });
      },
      onError: (err: ApiError) => {
        if (err.status === unauthorized) onUnauthorized();
      },
    },
  });
  const form = useForm({
    defaultValues: { name: account.name ?? "" },
    validators: { onChange: RenameConnectedAccountBody },
    onSubmitInvalid: () => formRef.current?.querySelector<HTMLInputElement>("input")?.focus(),
    onSubmit: ({ value, formApi }) => {
      if (rename.isPending) return;
      rename.mutate(
        { id: account.id, data: value },
        {
          onError: (err) => {
            const { fields } = formErrors(err, nameFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    listeners: {
      onChange: () => {
        if (!rename.isIdle) rename.reset();
      },
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const dirty = useSelector(form.store, (state) => state.isDirty);
  const failure = rename.error && rename.error.status !== unauthorized ? rename.error : null;
  const summary = failure ? formSummary(userMessage(failure), formErrors(failure, nameFields)) : [];

  useFocusAfterError(rename.error, rename.isPending, formRef, submitRef);

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-2"
      noValidate
      aria-label={`${nameLabel}, ${account.email}`}
      aria-busy={rename.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <LeaveWarning active={dirty && !rename.isPending} text={nameLeaveWarning} />
      <form.Field name="name">
        {(field) => {
          const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
          return (
            <Field data-invalid={errors.length > 0} data-disabled={rename.isPending}>
              <FieldLabel htmlFor={id}>{nameLabel}</FieldLabel>
              <div className="flex flex-wrap gap-2">
                <Input
                  id={id}
                  name="name"
                  className="min-w-0 flex-1 basis-48"
                  maxLength={renameConnectedAccountBodyNameMax}
                  autoComplete="off"
                  // Read-only, not disabled, while saving, so focus stays.
                  readOnly={rename.isPending}
                  aria-invalid={errors.length > 0}
                  aria-describedby={[describedBy, errors.length > 0 && `${id}-error`]
                    .filter(Boolean)
                    .join(" ")}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <Button
                  ref={submitRef}
                  type="submit"
                  variant="outline"
                  className="whitespace-normal"
                  isPending={rename.isPending}
                >
                  {rename.isPending ? savingNameText : saveNameText}
                </Button>
              </div>
              <FieldError id={`${id}-error`} errors={errors} />
            </Field>
          );
        }}
      </form.Field>
      {/* Stays mounted so screen readers announce it. */}
      <output className="text-sm text-muted-foreground">{rename.isSuccess ? savedText : ""}</output>
      <RequestError errors={summary} requestId={failure?.requestId} />
    </form>
  );
}
