import { useEffect, useRef, useState, type ReactElement } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import * as z from "zod";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListPlacesQueryKey,
  useCreateLabel,
  useEditLabel,
  useListConnectedAccounts,
} from "@/api/generated/inbok";
import type {
  LabelCreateInputBody as LabelCreateBody,
  LabelEditInputBody,
  LabelResource,
} from "@/api/generated/model";
import { LabelCreateInputBody } from "@/api/generated/model/labelCreateInputBody.zod";
import { userMessage } from "@/api/problem";
import { useAppForm } from "@/components/form/app-form";
import type { SelectOption } from "@/components/form/fields";
import { useFormRequest } from "@/components/form/request";
import { FocusFallback } from "@/components/confirm-dialog";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { accountDot, accountName } from "@/components/account-mark";
import { Dialog, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/cn";
import type { Label } from "./labels";
import {
  atTheTop,
  cancelLabelText,
  createAndMoveText,
  createText,
  creatingText,
  editLabelName,
  labelAccountLabel,
  labelNameLabel,
  nestUnderLabel,
  newLabelIn,
  newLabelName,
  saveLabelText,
  savingLabelText,
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

// LabelSave is what the form sends: a new label, or an edit of one.
type LabelSave =
  | { kind: "create"; data: LabelCreateBody }
  | { kind: "edit"; id: string; data: LabelEditInputBody };

// LabelStart is what a new label's form starts with: its account, name,
// and parent, e.g. what the member typed in a picker, split at the label
// it names (splitTyped).
export type LabelStart = { accountId: number; name: string; parent: string | null };

// LabelDialog is New label or Edit label (DESIGN.md "Sidebar", "Reading
// pane"), over the page while isOpen; onOpenChange closes it, as Cancel and
// Escape do, which send nothing. New label makes a label in one of
// accountIds, the accounts whose mail can change, starting from start; its
// Account field shows only with several, named and marked as the sidebar
// names them. Edit label renames editing, or moves it under another label:
// its own name and parent. labels are the member's, for the parents
// offered: those of the label's account, never editing or one inside it.
// action "create-and-move" names the button for a picker's Move to, which
// moves the conversation once the label is made. onDone gets the label
// saved, once the member's labels have been read again, so every list
// shows it. While it sends, Escape and Cancel wait: a sent change can't be
// called back. fallbackFocus is where focus goes once it has closed, when
// what opened it went, e.g. a label's ⋮ the label's move took away.
export function LabelDialog({
  isOpen,
  onOpenChange,
  fallbackFocus,
  ...form
}: {
  isOpen: boolean;
  onOpenChange: (isOpen: boolean) => void;
  fallbackFocus?: () => HTMLElement | null;
  labels: readonly Label[];
  accountIds: readonly number[];
  start?: LabelStart;
  editing?: Label;
  action?: "create" | "create-and-move";
  onDone: (label: Label) => void;
}): ReactElement {
  const [sending, setSending] = useState(false);
  return (
    <Dialog isOpen={isOpen} onOpenChange={onOpenChange} isKeyboardDismissDisabled={sending}>
      <FocusFallback target={fallbackFocus} />
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
  editing,
  action = "create",
  onDone,
  onSending,
  onClose,
}: {
  labels: readonly Label[];
  accountIds: readonly number[];
  start?: LabelStart;
  editing?: Label;
  action?: "create" | "create-and-move";
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
  // failing, or renamed some labels before refusing an edit. Returned, so the form waits for the read: once it
  // closes, the label shows wherever labels do.
  const reread = {
    onSettled: () => client.invalidateQueries({ queryKey: getListPlacesQueryKey() }),
  };
  const create = useCreateLabel({ mutation: { gcTime: 0, ...reread } });
  const edit = useEditLabel({ mutation: { gcTime: 0, ...reread } });
  const mutation = editing ? edit : create;
  const request = useFormRequest<LabelResource, LabelSave>(
    {
      mutateAsync: async (save) =>
        (save.kind === "edit"
          ? await edit.mutateAsync({ id: save.id, data: save.data })
          : await create.mutateAsync({ data: save.data })
        ).data,
      error: mutation.error,
    },
    labelFields,
  );
  const first = editing?.accountId ?? start?.accountId ?? accountIds[0] ?? 0;
  const form = useAppForm({
    defaultValues: {
      account: String(first),
      name: editing?.name ?? start?.name ?? "",
      parent: parentOf(editing, labels) ?? start?.parent ?? atTop,
    },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(labelValues) },
    onSubmit: async ({ value, formApi }) => {
      const parent = value.parent === atTop ? null : value.parent;
      const sent = await request.send(
        formApi,
        value,
        editing
          ? { kind: "edit", id: editing.place, data: { name: value.name, parent } }
          : {
              kind: "create",
              data: { accountId: Number(value.account), name: value.name, parent },
            },
      );
      if (sent.outcome !== "done") return;
      onClose();
      onDone(labelOf(sent.data, labels));
    },
    listeners: {
      onChange: ({ fieldApi }) => {
        if (mutation.error) mutation.reset();
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
    const input = ref.current?.querySelector<HTMLInputElement>("input[name=name]");
    // select() alone need not focus the field (MDN, "HTMLInputElement:
    // select() method"); an edited name is selected, to type over.
    input?.focus();
    if (editing) input?.select();
  }, [editing]);

  const title = editing
    ? editLabelName
    : accounts.length === 1
      ? newLabelIn(accountName(accounts[0]))
      : newLabelName;
  const button =
    editing === undefined
      ? {
          label: action === "create-and-move" ? createAndMoveText : createText,
          working: creatingText,
        }
      : { label: saveLabelText, working: savingLabelText };

  return (
    <div ref={ref} className="contents">
      <DialogHeader>
        <DialogTitle className="wrap-anywhere">{title}</DialogTitle>
      </DialogHeader>
      <form.AppForm>
        <form.Form label={title} className="gap-6">
          {!editing && accounts.length > 1 && (
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
                options={parentOptions(labels, accountId, editing)}
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
            <form.SubmitButton working={button.working}>{button.label}</form.SubmitButton>
          </DialogFooter>
        </form.Form>
      </form.AppForm>
    </div>
  );
}

// parentOf returns the place of label's parent among labels, atTop for a
// label at the top, or undefined without a label.
function parentOf(label: Label | undefined, labels: readonly Label[]): string | undefined {
  if (!label) return undefined;
  const parentPath = label.path.slice(0, -label.name.length - 1);
  if (!parentPath) return atTop;
  return (
    labels.find((l) => l.accountId === label.accountId && l.path === parentPath)?.place ?? atTop
  );
}

// parentOptions returns Nest under's choices: the top, then accountId's
// labels in the sidebar's order, each stepped in by its depth, but not
// editing or a label inside it, which can't hold it.
function parentOptions(
  labels: readonly Label[],
  accountId: number,
  editing: Label | undefined,
): SelectOption[] {
  const inside = (l: Label) =>
    editing !== undefined && (l.place === editing.place || l.path.startsWith(`${editing.path}/`));
  return [
    { value: atTop, label: atTheTop },
    ...labels
      .filter((l) => l.accountId === accountId && !inside(l))
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

// splitTyped reads typed, a path typed in a picker, as the picker shows
// paths: the longest path of accountId's labels that starts it, before a
// "/", is the parent, and the rest the name, e.g. "Clients/Initech" is
// Initech under Clients. Without one, the whole is the name.
export function splitTyped(
  typed: string,
  labels: readonly Label[],
  accountId: number,
): Omit<LabelStart, "accountId"> {
  let parent: Label | undefined;
  for (const label of labels) {
    if (label.accountId !== accountId) continue;
    if (!sameText(typed.slice(0, label.path.length + 1), `${label.path}/`)) continue;
    if (!parent || label.path.length > parent.path.length) parent = label;
  }
  return parent
    ? { name: typed.slice(parent.path.length + 1), parent: parent.place }
    : { name: typed, parent: null };
}

// Paths compare as Gmail compares names: ignoring case, not accents.
const collator = new Intl.Collator(undefined, { sensitivity: "accent" });

// sameText reports whether a and b are the same text, ignoring case.
export function sameText(a: string, b: string): boolean {
  return collator.compare(a, b) === 0;
}
