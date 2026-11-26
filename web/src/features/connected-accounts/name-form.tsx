import { useEffect, useRef, type ReactElement } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListConnectedAccountsQueryKey,
  useRenameConnectedAccount,
} from "@/api/generated/amplop";
import type { ListedAccount, ResponseConnectedAccounts } from "@/api/generated/model";
import { RenameConnectedAccountBody } from "@/api/generated/model/renameConnectedAccountBody.zod";
import { userMessage } from "@/api/problem";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { cancelText, nameDescription, nameLabel, saveText, savingText } from "./text";

const nameFields = ["name"] as const;

// NameForm sets what the member calls account, shown in mail instead of
// its address (DESIGN.md "Sidebar"); an empty name goes back to the
// address. It opens in the account's row with focus in the field, puts the
// server's answer into the accounts list, and calls onClose once saved, or
// on Cancel or Escape, which save nothing.
export function NameForm({
  account,
  onClose,
}: {
  account: ListedAccount;
  onClose: () => void;
}): ReactElement {
  const client = useQueryClient();
  const ref = useRef<HTMLDivElement>(null);
  const rename = useRenameConnectedAccount({
    mutation: {
      gcTime: 0,
      // Here, not in the form, so the list changes even if the form closed.
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
      },
    },
  });
  const request = useFormRequest(rename, nameFields);
  const form = useAppForm({
    defaultValues: { name: account.name ?? "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(RenameConnectedAccountBody) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { id: account.id, data: value });
      if (sent.outcome === "done") onClose();
    },
    listeners: {
      onChange: () => {
        if (rename.error) rename.reset();
      },
    },
  });
  const sending = useSelector(form.store, (state) => state.isSubmitting);

  // The latest onClose, which the Escape listener below calls, so the
  // listener is added once.
  const close = useRef(onClose);
  useEffect(() => {
    close.current = onClose;
  });

  useEffect(() => {
    const input = ref.current?.querySelector<HTMLInputElement>("input");
    // select() alone need not focus the field (MDN, "HTMLInputElement:
    // select() method").
    input?.focus();
    input?.select();
  }, []);

  useEffect(() => {
    const box = ref.current;
    if (!box) return undefined;
    // Escape cancels, as Cancel does; a sent name can't be called back, so
    // it waits for the answer as Cancel does.
    function cancel(event: KeyboardEvent): void {
      if (event.key !== "Escape" || form.state.isSubmitting) return;
      event.preventDefault();
      close.current();
    }
    // NOTE: a native listener, not onKeyDown on the box: the lint rule
    // jsx-a11y/no-static-element-interactions refuses a key handler on a
    // div, and the box only catches Escape bubbling from the field and the
    // buttons inside it.
    box.addEventListener("keydown", cancel);
    return () => box.removeEventListener("keydown", cancel);
  }, [form]);

  return (
    <div ref={ref} className="w-full max-w-sm">
      <form.AppForm>
        <form.Form label={`${nameLabel}, ${account.email}`} className="gap-4">
          <form.AppField name="name">
            {(field) => (
              <field.TextField
                label={nameLabel}
                description={nameDescription}
                optional
                autoComplete="off"
              />
            )}
          </form.AppField>
          <div className="flex flex-wrap gap-3">
            <form.SubmitButton working={savingText}>{saveText}</form.SubmitButton>
            {/* Waits while a save sends: a sent name can't be called back. */}
            <Button type="button" variant="outline" isDisabled={sending} onPress={onClose}>
              {cancelText}
            </Button>
          </div>
          <RequestError errors={request.summary(userMessage)} requestId={request.reference} />
        </form.Form>
      </form.AppForm>
    </div>
  );
}
