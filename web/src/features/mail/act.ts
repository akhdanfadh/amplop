import { useEffect, useRef } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import type { ActionMail } from "./actions";
import type { Conversation } from "./conversations";
import { requestRowFocus } from "./focus-request";
import { perform, type Performed } from "./perform";
import type { AppHref, Place } from "./places";
import { inList, leavesList, staysInList } from "./rules";

// ReaderList is the list a conversation was opened from: one place's, or
// a search's, which keeps a conversation in any of its places, of account
// alone when given; key names it for its focus requests (listKey), and
// back is its address.
export type ReaderList = {
  places: readonly Place[];
  account: number | undefined;
  key: string;
  back: AppHref;
};

// offeredIn returns the list whose actions a conversation of a list of
// places offers (actionsFor): its one place, e.g. Spam's, or, for a search
// of several, "all", which follows the conversation's own places.
export function offeredIn(places: readonly Place[]): Place {
  return places.length === 1 ? places[0] : "all";
}

// useAct returns act, which runs an action on a conversation of list, in
// mail, from the reading pane or from the conversation's row, and goBack,
// which takes the reading pane back to list. openId is the conversation
// the reading pane shows.
//
// On the open conversation, from the pane or its row, an action that takes
// it out of list, as the guess shows it, goes back to the list at once,
// where the row that took its place takes focus, or the row itself after
// Mark unread; a refusal then says why on the list. One the provider no
// longer has goes back too, its row gone, while the address is still the
// one it was run at. On another row, an action that takes it out of list
// gives focus to the row that takes its place, the pane staying as it is.
export function useAct(
  mail: ActionMail | undefined,
  list: ReaderList,
  openId: string | undefined,
): {
  act: (row: Conversation, act: Performed) => void;
  goBack: () => void;
} {
  const navigate = useNavigate();
  const router = useRouter();
  const { places, account, key, back } = list;
  // Back returns to the list the member came from, as the phone's own back
  // does, so it adds no history, when a row of it opened the conversation;
  // one opened another way, e.g. from a link or Settings' Back to mail,
  // goes to its list in its place, rather than back out of mail.
  const goBack = () => {
    if (router.state.location.state.fromList) router.history.back();
    else void navigate({ ...back, replace: true });
  };
  // The open conversation an action took out of its list, which takes no
  // more actions: going back takes a moment, and a second press of a
  // double click would land on the button's opposite, e.g. Move to Inbox.
  const left = useRef<string | null>(null);
  // Read at the press: a list's rows keep the act they were drawn with.
  const opened = useRef(openId);
  useEffect(() => {
    opened.current = openId;
    left.current = null;
  }, [openId]);

  const act = (row: Conversation, performed: Performed) => {
    if (!mail || left.current === row.id) return;
    const open = row.id === opened.current;
    const leaves = open
      ? leavesList(row, performed, places, account)
      : inList(row, places, account) && !staysInList(row, performed, places, account);
    // The address, not the pane's drawn conversation: a row chosen since
    // moves the address at once, while the pane draws this conversation
    // until the next one's page has loaded.
    const at = router.state.location.href;
    void perform({ mail, conversations: [row], act: performed }).then((outcomes) => {
      const gone = outcomes.get(row.id) === "gone";
      if (open && gone && !leaves && router.state.location.href === at) goBack();
    });
    if (!leaves) return;
    requestRowFocus(key, {
      id: row.id,
      dateMs: row.dateMs,
      stays: staysInList(row, performed, places, account),
      now: !open,
    });
    if (!open) return;
    left.current = row.id;
    goBack();
  };
  return { act, goBack };
}
