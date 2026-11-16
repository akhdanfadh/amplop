import type { Action } from "./rules";

// Slot is a button's place among a conversation's actions: the actions
// that take turns in it as conversations change, e.g. Archive and Move to
// Inbox, keep one button, so focus stays on it as its action changes.
export type Slot = "move" | "delete" | "mark" | "star";

// The slots in order, each with its actions, the usual one first.
const slots: readonly (readonly [Slot, readonly Action[]])[] = [
  ["move", ["archive", "unarchive", "unspam", "untrash"]],
  ["delete", ["trash"]],
  ["mark", ["read", "unread"]],
  ["star", ["star", "unstar"]],
];

// The action kept in More, chosen by hand (DESIGN.md "Reading pane"):
// reported less often, and a wrong press sends the conversation out of
// sight.
const inMore: Action = "spam";

// laidOut returns offered, actionsFor's answer, as the reading pane's
// toolbar, the picking bar, and a hovered row show it: a button per slot
// with its first action offered, in slot order, and the actions More
// holds, a slot's other one where mixed rows offer both, then Report
// spam.
export function laidOut(offered: readonly Action[]): {
  buttons: { slot: Slot; action: Action }[];
  more: Action[];
} {
  const buttons: { slot: Slot; action: Action }[] = [];
  const more: Action[] = [];
  for (const [slot, actions] of slots) {
    const here = actions.filter((action) => offered.includes(action));
    if (here.length === 0) continue;
    buttons.push({ slot, action: here[0] });
    more.push(...here.slice(1));
  }
  if (offered.includes(inMore)) more.push(inMore);
  return { buttons, more };
}
