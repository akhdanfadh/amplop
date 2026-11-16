import { createContext, useContext, useSyncExternalStore } from "react";
import type { OutgoingMessage } from "@/api/generated/model";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import type { LeaveState } from "./text";

// One new message the compose window holds: id is new for each message,
// so its editor starts afresh; window is the window's, kept while one
// message replaces another in it, e.g. by Edit and send, so its layout and
// history entry stay; edit is the sent message it starts from.
//
// DESIGN: window is a UUID, unique beyond this page load: a phone's
// history entry keeps it through a reload, e.g. when another tab signs a
// member out, and a counter starting again at 1 would let a new window
// take an old entry's id, so Back from it would not close it.
export type ComposeMessage = { id: number; window: string; edit?: OutgoingMessage };

// What the compose window shows and holds, as ComposeStore keeps it.
export type ComposeState = {
  // The message the window holds; null while it is closed.
  message: ComposeMessage | null;
  // Counts the opens that brought back the message already held, so the
  // window can restore itself and take focus.
  shown: number;
  // What closing the window now would lose: typed text, a send running,
  // or a try that may have saved the message; null when nothing.
  loses: LeaveState | null;
  // A sent message whose Edit and send waits for the member to agree to
  // replace the text the window holds.
  replacing: OutgoingMessage | null;
  // What had focus when the window opened, for focus to return to.
  opener: HTMLElement | null;
  // Whether the window covers the page, full screen, which the page is
  // then inert behind.
  covering: boolean;
};

const closed: ComposeState = {
  message: null,
  shown: 0,
  loses: null,
  replacing: null,
  opener: null,
  covering: false,
};

// ComposeStore owns the compose window (DESIGN.md "Compose and reply"):
// whether it is open and what it holds. One per router, in its context, so
// a route can open it before it draws, e.g. /compose, and each test starts
// with a closed window. Components read it with useCompose.
export class ComposeStore {
  private state: ComposeState = closed;

  private nextId = 1;
  private readonly listeners = new Set<() => void>();

  constructor() {
    // The message is one member's data: it ends with theirs, e.g. on
    // sign-out, so the next member to sign in in this tab never sees it.
    // The store lives as long as its router, so it never unsubscribes.
    onAccountCacheClear(() => this.close());
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  snapshot = (): ComposeState => this.state;

  // open opens the window on a new message, empty or starting from edit's
  // content. An open window holding nothing to lose, e.g. a sent message's
  // status, takes the new message; one holding typed text comes back as it
  // was instead, and with edit, asks first whether to replace it
  // (replacing). Either way the window comes back (shown).
  open = (edit?: OutgoingMessage): void => {
    const { message, loses, shown } = this.state;
    if (message === null) {
      const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      const next = { id: this.nextId++, window: crypto.randomUUID(), edit };
      this.set({ ...closed, message: next, shown, opener });
      return;
    }
    if (loses === null) {
      const next = { id: this.nextId++, window: message.window, edit };
      this.set({ ...this.state, message: next, shown: shown + 1 });
      return;
    }
    this.set({ ...this.state, shown: shown + 1, replacing: edit ?? this.state.replacing });
  };

  // replace starts the message replacing waits for, dropping the text the
  // window holds; keep drops replacing instead.
  replace = (): void => {
    const { replacing: edit, message } = this.state;
    if (!edit || !message) return;
    const next = { id: this.nextId++, window: message.window, edit };
    this.set({ ...this.state, message: next, loses: null, replacing: null });
  };

  keep = (): void => this.set({ ...this.state, replacing: null });

  // close closes the window, dropping what it holds.
  close = (): void => this.set({ ...closed, shown: this.state.shown });

  // setCovering records whether the window covers the page.
  setCovering = (covering: boolean): void => {
    if (this.state.covering !== covering) this.set({ ...this.state, covering });
  };

  // setLoses records what closing the window would lose now.
  setLoses = (loses: LeaveState | null): void => {
    if (this.state.loses !== loses) this.set({ ...this.state, loses });
  };

  private set(state: ComposeState): void {
    this.state = state;
    for (const listener of this.listeners) listener();
  }
}

// The app's store, given to the signed-in pages by ComposeProvider. The
// default serves a component drawn without it, e.g. in a test of the
// sidebar alone.
const ComposeContext = createContext<ComposeStore>(new ComposeStore());

export const ComposeProvider = ComposeContext.Provider;

// useCompose returns the compose window's store and its state now,
// rendering again when it changes.
export function useCompose(): { store: ComposeStore; state: ComposeState } {
  const store = useContext(ComposeContext);
  const state = useSyncExternalStore(store.subscribe, store.snapshot, store.snapshot);
  return { store, state };
}
