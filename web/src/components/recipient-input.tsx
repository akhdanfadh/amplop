import { useState, type KeyboardEvent, type ReactElement } from "react";
import { Button as AriaButton, Tag, TagGroup, TagList } from "react-aria-components";
import { XIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import { parseRecipients } from "@/lib/recipients";

// What a chip adds after a value that isn't an address.
const notAnAddress = "not an email address";

// Whether a recipient field's input holds text not yet a chip.
export type Typing = "typing" | "idle";

// Keys that end an address being typed, as in Gmail. Enter is one only
// while text is typed, so an empty field leaves it to the form.
const separatorKeys = new Set([",", ";"]);

// RecipientInput is one recipient list as chips, one per address, then an
// input whose text becomes chips on a comma, semicolon, or Enter, on
// leaving, and on pasting a list. id is the input's, for its label; label
// names the chips for assistive technology. isAddress tells an address
// from text that isn't one, by the contract's rule. value is the list and
// onChange receives the next one; onBlur runs when focus leaves the input,
// after its text became chips. invalid marks the field; describedBy names
// its description and error. readOnly locks it, e.g. while sending: no
// typing, paste, or removal changes the list. onKeyDown sees every key the
// input does not take, e.g. a send shortcut, after typed text became chips.
// onTyping reports whether the input holds text not yet a chip
// ("typing") or none ("idle"); a form counts typing as an edit, e.g.
// before discarding.
//
// DESIGN: an address the contract refuses stays a chip, marked and named
// "not an email address", so the member fixes or removes it in place; the
// form's error says which. React Aria's TagGroup gives the chips one Tab
// stop, arrows between them, and Delete or Backspace to remove one.
export function RecipientInput({
  id,
  label,
  isAddress,
  value,
  onChange,
  onBlur,
  invalid = false,
  describedBy,
  readOnly = false,
  onKeyDown,
  onTyping,
  className,
}: {
  id: string;
  label: string;
  isAddress: (value: string) => boolean;
  value: string[];
  onChange: (value: string[]) => void;
  onBlur?: () => void;
  invalid?: boolean;
  describedBy?: string;
  readOnly?: boolean;
  onKeyDown?: (event: KeyboardEvent<HTMLInputElement>) => void;
  onTyping?: (state: Typing) => void;
  className?: string;
}): ReactElement {
  const [text, setText] = useState("");
  const keys = chipKeys(value);

  // type sets the input's text and reports whether any is left unkept.
  function type(next: string): void {
    setText(next);
    onTyping?.(next.trim() === "" ? "idle" : "typing");
  }

  // keep adds the addresses in typed, if any, as chips.
  function keep(typed: string): void {
    if (readOnly) return;
    const added = parseRecipients(typed);
    type("");
    if (added.length > 0) onChange([...value, ...added]);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    // A shortcut, e.g. Ctrl+Enter to send, keeps what was typed first, so
    // it is part of what the shortcut acts on.
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
      keep(text);
      onKeyDown?.(event);
      return;
    }
    if (separatorKeys.has(event.key) || (event.key === "Enter" && text.trim() !== "")) {
      event.preventDefault();
      keep(text);
      return;
    }
    if (event.key === "Backspace" && text === "" && value.length > 0 && !readOnly) {
      event.preventDefault();
      onChange(value.slice(0, -1));
      return;
    }
    onKeyDown?.(event);
  }

  return (
    <div className={cn("flex min-w-0 flex-1 flex-wrap items-center gap-1", className)}>
      {value.length > 0 && (
        <TagGroup
          aria-label={label}
          onRemove={(removed) => {
            if (!readOnly) onChange(value.filter((_, index) => !removed.has(keys[index])));
          }}
          disabledKeys={readOnly ? keys : undefined}
          className="contents"
        >
          <TagList className="contents">
            {value.map((address, index) => (
              <Chip
                key={keys[index]}
                id={keys[index]}
                address={address}
                valid={isAddress(address)}
              />
            ))}
          </TagList>
        </TagGroup>
      )}
      <input
        id={id}
        type="text"
        inputMode="email"
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        readOnly={readOnly}
        aria-invalid={invalid}
        aria-describedby={describedBy}
        value={text}
        onChange={(event) => type(event.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={() => {
          keep(text);
          onBlur?.();
        }}
        onPaste={(event) => {
          if (readOnly) return;
          const pasted = event.clipboardData.getData("text");
          // A single address pastes as typed text, to edit before it is kept.
          if (parseRecipients(pasted).length < 2) return;
          event.preventDefault();
          keep(`${text},${pasted}`);
        }}
        className="h-7 min-w-32 flex-1 bg-transparent px-1 text-base outline-none read-only:cursor-default md:text-sm pointer-coarse:h-9"
      />
    </div>
  );
}

// withTyping returns lists, the names of the recipient lists holding text
// not yet a chip, with name added while state is "typing" and removed
// otherwise.
export function withTyping(
  lists: ReadonlySet<string>,
  name: string,
  state: Typing,
): ReadonlySet<string> {
  const typing = state === "typing";
  if (lists.has(name) === typing) return lists;
  const next = new Set(lists);
  if (typing) next.add(name);
  else next.delete(name);
  return next;
}

// chipKeys returns a key for each address in value: the address and how
// many times it came before, so a repeated address keeps its own chip.
function chipKeys(value: string[]): string[] {
  const seen = new Map<string, number>();
  return value.map((address) => {
    const count = seen.get(address) ?? 0;
    seen.set(address, count + 1);
    return `${count}:${address}`;
  });
}

// Chip is one address in a RecipientInput, marked when valid is false.
function Chip({
  id,
  address,
  valid,
}: {
  id: string;
  address: string;
  valid: boolean;
}): ReactElement {
  return (
    <Tag
      id={id}
      textValue={valid ? address : `${address}, ${notAnAddress}`}
      className={cn(
        "inline-flex h-7 max-w-full cursor-default items-center gap-0.5 rounded-full pr-0.5 pl-2.5 text-sm outline-none data-focus-visible:focus-ring pointer-coarse:h-9",
        valid ? "bg-secondary text-foreground" : "bg-destructive/8 text-destructive",
      )}
    >
      {/* The whole address on hover when a long one is cut short. */}
      <span className="truncate" translate="no" title={address}>
        {address}
      </span>
      {!valid && <span className="sr-only">, {notAnAddress}</span>}
      {/* React Aria names it "Remove" and the chip's name. Out of the Tab
          order, as React Spectrum's tags are: one Tab stop reaches the
          chips, and Delete or Backspace removes one, which React Aria
          tells keyboard users. */}
      <AriaButton
        slot="remove"
        excludeFromTabOrder
        // A 44 px touch target around the small button (DESIGN.md
        // "Control height"), as the sidebar's actions have.
        className="relative grid size-6 shrink-0 place-items-center rounded-full transition-colors outline-none after:absolute after:-inset-1.5 hover:bg-accent data-focus-visible:focus-ring pointer-coarse:size-8 pointer-fine:after:hidden data-disabled:hidden"
      >
        <XIcon className="size-3.5" aria-hidden="true" />
      </AriaButton>
    </Tag>
  );
}
