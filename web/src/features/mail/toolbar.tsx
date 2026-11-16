import { useRef, useState, type ReactElement } from "react";
import { ToggleButton } from "react-aria-components";
import { EllipsisVerticalIcon, ExternalLinkIcon } from "lucide-react";
import { Button, buttonVariants } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import type { Conversation } from "./conversations";
import { LabelMenuItems, LabelPicker, type Picker } from "./label-menu";
import type { Label } from "./labels";
import type { Performed } from "./perform";
import type { Place } from "./places";
import { actionsFor, type Action } from "./rules";
import { moreName, openInGmail, opensInNewTab } from "./text";
import { actionWords } from "./words";

// The action a toolbar keeps in More, chosen by hand (DESIGN.md "Reading
// pane"): reported less often, and a wrong press sends the conversation
// out of sight.
const inMore: Action = "spam";

// The toolbar's slot of each action that trades places with another as the
// conversation changes, e.g. Archive with Move to Inbox: one slot, one
// button, so focus stays on it as its action changes. The others are
// their own slot.
const slots: Partial<Record<Action, string>> = {
  archive: "move",
  unarchive: "move",
  untrash: "move",
  unspam: "move",
  read: "mark",
  unread: "mark",
};

// ConversationToolbar is a conversation's actions in the reading pane's
// toolbar (DESIGN.md "Reading pane"), as its state in list offers them:
// the state's first action, Delete, Mark read or unread, Star, then More
// with Move to… and Labels…, which open their picker under More
// (LabelPicker, of labels, under their account's name when headings is
// set), Report spam, and Open in Gmail, providerUrl, in a new tab.
// onAction runs an action or a change; accountName names an account, and
// canChange reports whether its labels can change (LabelPicker).
export function ConversationToolbar({
  conversation,
  list,
  providerUrl,
  headings,
  labels,
  accountName,
  canChange,
  onAction,
}: {
  conversation: Conversation;
  list: Place;
  providerUrl: string | null | undefined;
  headings: boolean;
  labels: readonly Label[];
  accountName: (accountId: number) => string;
  canChange: (accountId: number) => boolean;
  onAction: (act: Performed) => void;
}): ReactElement {
  const actions = actionsFor([conversation], list);
  const more = actions.filter((action) => action === inMore);
  const gmail = providerUrl && isHttps(providerUrl) ? providerUrl : undefined;
  // The label picker More opened, under More's button.
  const [picker, setPicker] = useState<Picker | null>(null);
  const moreButton = useRef<HTMLButtonElement>(null);
  return (
    <>
      {actions
        .filter((action) => action !== inMore)
        .map((action) =>
          action === "star" || action === "unstar" ? (
            <StarToggle
              key="star"
              starred={conversation.starred}
              onPress={() => onAction(action)}
            />
          ) : (
            <ActionButton key={slots[action] ?? action} action={action} onAction={onAction} />
          ),
        )}
      <DropdownMenuTrigger>
        <TooltipTrigger>
          <Button ref={moreButton} variant="ghost" size="icon" aria-label={moreName}>
            <EllipsisVerticalIcon aria-hidden="true" />
          </Button>
          <Tooltip>{moreName}</Tooltip>
        </TooltipTrigger>
        <DropdownMenu placement="bottom end" className="w-auto">
          <LabelMenuItems conversations={[conversation]} list={list} onOpen={setPicker} />
          {more.map((action) => {
            const { label, icon: Icon } = actionWords[action];
            return (
              <DropdownMenuItem key={action} onAction={() => onAction(action)}>
                <Icon aria-hidden="true" />
                {label}
              </DropdownMenuItem>
            );
          })}
          {gmail && (
            <DropdownMenuItem
              href={gmail}
              target="_blank"
              rel="noopener noreferrer"
              textValue={`${openInGmail} ${opensInNewTab}`}
            >
              <ExternalLinkIcon aria-hidden="true" />
              {openInGmail}
              <span className="sr-only">{opensInNewTab}</span>
            </DropdownMenuItem>
          )}
        </DropdownMenu>
      </DropdownMenuTrigger>
      <LabelPicker
        picker={picker}
        triggerRef={moreButton}
        onClose={() => setPicker(null)}
        conversations={[conversation]}
        list={list}
        headings={headings}
        labels={labels}
        accountName={accountName}
        canChange={canChange}
        onAct={(act) => onAction(act)}
      />
    </>
  );
}

// isHttps reports whether url is a secure web address, the only kind the
// toolbar links to.
function isHttps(url: string): url is `https://${string}` {
  return url.startsWith("https://");
}

// ActionButton is one action's icon button, named by its tooltip's words.
function ActionButton({
  action,
  onAction,
}: {
  action: Action;
  onAction: (action: Action) => void;
}): ReactElement {
  const { label, icon: Icon } = actionWords[action];
  return (
    <TooltipTrigger>
      <Button variant="ghost" size="icon" aria-label={label} onPress={() => onAction(action)}>
        <Icon aria-hidden="true" />
      </Button>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

// StarToggle stars or unstars the conversation, its pressed state the
// star.
function StarToggle({ starred, onPress }: { starred: boolean; onPress: () => void }): ReactElement {
  const { label, icon: Icon } = actionWords.star;
  return (
    <TooltipTrigger>
      <ToggleButton
        aria-label={label}
        isSelected={starred}
        onPress={onPress}
        className={buttonVariants({ variant: "ghost", size: "icon" })}
      >
        <Icon aria-hidden="true" className={cn(starred && "fill-current")} />
      </ToggleButton>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}
