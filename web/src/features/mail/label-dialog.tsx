import { useEffect, useRef, useState, type ReactElement } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import * as z from "zod";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListPlacesQueryKey,
  useCreateLabel,
  useListConnectedAccounts,
} from "@/api/generated/inbok";
import type { LabelCreateInputBody as LabelCreateBody, LabelResource } from "@/api/generated/model";
import { LabelCreateInputBody } from "@/api/generated/model/labelCreateInputBody.zod";
import { userMessage } from "@/api/problem";
import { useAppForm } from "@/components/form/app-form";
import type { SelectOption } from "@/components/form/fields";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { accountDot, accountName } from "@/components/account-mark";
import { Dialog, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/cn";
import type { Label } from "./labels";
import {
  atTheTop,
  cancelLabelText,
  createText,
  creatingText,
  labelAccountLabel,
  labelNameLabel,
  nestUnderLabel,
  newLabelIn,
  newLabelName,
} from "./text";

// The fields a refusal can name; the account's goes in the summary.
const labelFields = ["name", "parent"] as const;

// The Nest under choice of no parent, which no label's id can be.
const atTop = "top";

// The form's values: only the name is checked here, against the
// contract's rules; the server checks the rest, e.g. the separator, and
// its refusal marks the field.
const labelValues = LabelCreateInputBody.pick({ name: true }).extend({
  account: z.string(),
  parent: z.string(),
});

// LabelStart is what a new label's form starts with: its account, name,
// and parent.
export type LabelStart = { accountId: number; name: string; parent: string | null };

// LabelDialog is New label (DESIGN.md "Sidebar"), over the page while
// isOpen; onOpenChange closes it, as Cancel and Escape do, which send
// nothing. It makes a label in one of accountIds, the accounts whose mail
// can change, starting from start; its Account field shows only with
// several, named and marked as the sidebar names them. labels are the
// member's, for the parents offered: those of the chosen account. onDone
// gets the label made, once the member's labels have been read again, so
// every list shows it. While it sends, Escape and Cancel wait: a sent
// change can't be called back.
export function LabelDialog({
  isOpen,
  onOpenChange,
  ...form
}: {
  isOpen: boolean;
  onOpenChange: (isOpen: boolean) => void;
  labels: readonly Label[];
  accountIds: readonly number[];
  start?: LabelStart;
  onDone: (label: Label) => void;
}): ReactElement {
  const [sending, setSending] = useState(false);
  return (
    <Dialog isOpen={isOpen} onOpenChange={onOpenChange} isKeyboardDismissDisabled={sending}>
      {/* Mounted while open, so each opening starts from its own values. */}
      <LabelForm {...form} onSending={setSending} onClose={() => onOpenChange(false)} />
    </Dialog>
  );
}

// LabelForm is LabelDialog's form, of its props, closing it with onClose
// and saying through onSending whether it is sending.
function LabelForm({
  labels,
  accountIds,
  start,
  onDone,
  onSending,
  onClose,
}: {
  labels: readonly Label[];
  accountIds: readonly number[];
  start?: LabelStart;
  onDone: (label: Label) => void;
  onSending: (sending: boolean) => void;
  onClose: () => void;
}): ReactElement {
  const client = useQueryClient();
  const listed = useListConnectedAccounts().data?.data.accounts;
  const accounts = (listed ?? []).filter((a) => accountIds.includes(a.id));
  const ref = useRef<HTMLDivElement>(null);
  // Here, not in the form, so the lists read the change even if the form
  // closed, after a refusal too: Gmail may have made the label before
  // failing. Returned, so the form waits for the read: once it closes, the
  // label shows wherever labels do.
  const create = useCreateLabel({
    mutation: {
      gcTime: 0,
      onSettled: () => client.invalidateQueries({ queryKey: getListPlacesQueryKey() }),
    },
  });
  const request = useFormRequest(
    {
      mutateAsync: async (data: LabelCreateBody) => (await create.mutateAsync({ data })).data,
      error: create.error,
    },
    labelFields,
  );
  const form = useAppForm({
    defaultValues: {
      account: String(start?.accountId ?? accountIds[0] ?? 0),
      name: start?.name ?? "",
      parent: start?.parent ?? atTop,
    },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(labelValues) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, {
        accountId: Number(value.account),
        name: value.name,
        parent: value.parent === atTop ? null : value.parent,
      });
      if (sent.outcome !== "done") return;
      onClose();
      onDone(labelOf(sent.data, labels));
    },
    listeners: {
      onChange: ({ fieldApi }) => {
        if (create.error) create.reset();
        // Another account's labels are no parents for this one.
        if (fieldApi.name === "account") fieldApi.form.setFieldValue("parent", atTop);
      },
    },
  });
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  useEffect(() => {
    onSending(sending);
    return () => onSending(false);
  }, [sending, onSending]);
  const accountId = Number(useSelector(form.store, (state) => state.values.account));

  useEffect(() => {
    ref.current?.querySelector<HTMLInputElement>("input[name=name]")?.focus();
  }, []);

  const title = accounts.length === 1 ? newLabelIn(accountName(accounts[0])) : newLabelName;

  return (
    <div ref={ref} className="contents">
      <DialogHeader>
        <DialogTitle className="wrap-anywhere">{title}</DialogTitle>
      </DialogHeader>
      <form.AppForm>
        <form.Form label={title} className="gap-6">
          {accounts.length > 1 && (
            <form.AppField name="account">
              {(field) => (
                <field.SelectField
                  label={labelAccountLabel}
                  options={accounts.map((a) => ({
                    value: String(a.id),
                    label: accountName(a),
                    // The account's mark, as everywhere it is named
                    // (DESIGN.md "Colour").
                    before: (
                      <span
                        aria-hidden="true"
                        className={cn(
                          "size-2 shrink-0 self-center rounded-full",
                          accountDot(a.color),
                        )}
                      />
                    ),
                  }))}
                />
              )}
            </form.AppField>
          )}
          <form.AppField name="name">
            {(field) => <field.TextField label={labelNameLabel} autoComplete="off" />}
          </form.AppField>
          <form.AppField name="parent">
            {(field) => (
              <field.SelectField
                label={nestUnderLabel}
                optional
                options={parentOptions(labels, accountId)}
              />
            )}
          </form.AppField>
          <RequestError errors={request.summary(userMessage)} requestId={request.reference} />
          <DialogFooter>
            {/* Waits while the label sends: a sent change can't be called
                back. */}
            <Button type="button" variant="outline" isDisabled={sending} onPress={onClose}>
              {cancelLabelText}
            </Button>
            <form.SubmitButton working={creatingText}>{createText}</form.SubmitButton>
          </DialogFooter>
        </form.Form>
      </form.AppForm>
    </div>
  );
}

// parentOptions returns Nest under's choices: the top, then accountId's
// labels in the sidebar's order, each stepped in by its depth and named
// whole by its path.
function parentOptions(labels: readonly Label[], accountId: number): SelectOption[] {
  return [
    { value: atTop, label: atTheTop },
    ...labels
      .filter((l) => l.accountId === accountId)
      .map((l) => ({ value: l.place, label: l.name, whole: l.path, depth: l.depth })),
  ];
}

// labelOf returns saved, as the API answers a label, as menus show it: its
// path and depth under its parent among labels.
function labelOf(saved: LabelResource, labels: readonly Label[]): Label {
  const parent = saved.parent === null ? undefined : labels.find((l) => l.place === saved.parent);
  return {
    place: saved.id,
    accountId: saved.accountId,
    name: saved.name,
    path: parent ? `${parent.path}/${saved.name}` : saved.name,
    depth: parent ? parent.depth + 1 : 0,
  };
}
