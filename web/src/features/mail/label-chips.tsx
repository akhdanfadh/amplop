import { useRef, type ReactElement } from "react";
import { Button as AriaButton, Tag, TagGroup, TagList } from "react-aria-components";
import { linkOptions } from "@tanstack/react-router";
import { XIcon } from "lucide-react";
import { badgeVariants } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import type { Conversation } from "./conversations";
import type { Label } from "./labels";
import { PathName } from "./label-menu";
import { inboxChip, marksName } from "./text";

// The Inbox's chip's key, which no label's id can be.
const inboxKey = "inbox";

// LabelChips marks under the reading pane's title where conversation is
// (DESIGN.md "Reading pane"): the Inbox while it is in it, then each of
// its labels, of labels, the member's in the sidebar's order, by its
// path. Each chip opens its list, of listAccount alone for the Inbox, as
// the list it came from shows, and of the label's account for a label;
// each has an × (or Delete) that sends at once: onArchive for the Inbox,
// as Archive does, onRemove for a label. Nothing shows for a conversation
// in neither; when the last chip goes, focus goes to the title.
export function LabelChips({
  conversation,
  labels,
  listAccount,
  onArchive,
  onRemove,
}: {
  conversation: Conversation;
  labels: readonly Label[];
  listAccount: number | undefined;
  onArchive: () => void;
  onRemove: (label: Label) => void;
}): ReactElement | null {
  const own = labels.filter(
    (l) => l.accountId === conversation.accountId && conversation.labelIds.includes(l.place),
  );
  const group = useRef<HTMLDivElement>(null);
  if (!conversation.inbox && own.length === 0) return null;
  const chips = own.length + (conversation.inbox ? 1 : 0);
  return (
    <TagGroup
      ref={group}
      aria-label={marksName}
      onRemove={(keys) => {
        // React Aria moves focus to a chip beside the one removed; with
        // none left, the group goes, and focus with it to the page.
        const article = group.current?.closest("article");
        if (keys.size >= chips) {
          requestAnimationFrame(() => {
            if (document.activeElement !== document.body) return;
            article?.querySelector<HTMLElement>("h2[tabindex]")?.focus();
          });
        }
        for (const key of keys) {
          if (key === inboxKey) onArchive();
          const label = own.find((l) => l.place === key);
          if (label) onRemove(label);
        }
      }}
      className="pt-1"
    >
      <TagList className="flex flex-wrap gap-2">
        {conversation.inbox && (
          <Chip
            id={inboxKey}
            parents=""
            name={inboxChip}
            href={linkOptions({
              to: "/mail/$place",
              params: { place: inboxKey },
              search: { account: listAccount },
            })}
          />
        )}
        {own.map((label) => (
          <Chip
            key={label.place}
            id={label.place}
            parents={label.path.slice(0, label.path.length - label.name.length)}
            name={label.name}
            href={linkOptions({
              to: "/mail/$place",
              params: { place: label.place },
              search: { account: label.accountId },
            })}
          />
        ))}
      </TagList>
    </TagGroup>
  );
}

// Chip is one place's chip: the list rows' Inbox chip's badge, a link to
// its list, named by its path, parents then its own name, ended by its ×.
function Chip({
  id,
  parents,
  name,
  href,
}: {
  id: string;
  parents: string;
  name: string;
  href: NonNullable<Parameters<typeof Tag>[0]["href"]>;
}): ReactElement {
  return (
    <Tag
      id={id}
      textValue={parents + name}
      href={href}
      className={cn(
        badgeVariants({ variant: "secondary" }),
        // The recipient chips' sizes to touch (DESIGN.md "Control height"),
        // and room for the ×'s target, which the badge would clip; the ×
        // ends the chip, its own inset its padding, as theirs does.
        // A long path wraps inside the chip, which then grows a line and
        // keeps its corners rounded rather than a pill's.
        "h-auto min-h-5 max-w-full cursor-pointer justify-start gap-0.5 overflow-visible rounded-lg pr-0.5 whitespace-normal outline-none hover:bg-foreground/10 data-focus-visible:focus-ring pointer-coarse:min-h-9 pointer-coarse:pl-3 pointer-coarse:text-sm",
      )}
    >
      <PathName parents={parents} name={name} />
      {/* React Aria names it "Remove" and the chip's name. Out of the Tab
          order, as the recipient chips' are: one Tab stop reaches the
          chips, and Delete or Backspace removes one. */}
      <AriaButton
        slot="remove"
        excludeFromTabOrder
        // A 44 px touch target around the button, as the recipient chips'
        // has (DESIGN.md "Control height").
        className="relative grid size-4 shrink-0 cursor-default place-items-center rounded-full transition-colors outline-none after:absolute after:-inset-1.5 hover:bg-foreground/10 data-focus-visible:focus-ring pointer-coarse:size-8 pointer-fine:after:hidden"
      >
        <XIcon className="size-3 pointer-coarse:size-3.5" aria-hidden="true" />
      </AriaButton>
    </Tag>
  );
}
