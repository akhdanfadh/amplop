// Which messages of a conversation show open, folded to one line, or
// counted in "N more messages" (DESIGN.md "Reading pane").

// The newest messages that always show, open or folded.
const keptNewest = 3;

// The fewest folded messages worth hiding behind "N more messages": one
// row in place of one row saves nothing.
const minHidden = 2;

// A message as folding needs it.
type Foldable = { id: string; unread: boolean };

// Whether "N more messages" rows gather the folded messages between the
// first and the newest, or every message has its own row.
export type Folding = "grouped" | "all";

// A row of the conversation: one message, or the folded messages that
// "N more messages" stands for.
export type FoldedRow<M> = { kind: "message"; message: M } | { kind: "more"; messages: M[] };

// initiallyOpen returns the IDs of messages, oldest first, that open when
// the conversation does: the newest, and every unread one.
export function initiallyOpen(messages: readonly Foldable[]): Set<string> {
  const newest = messages.at(-1);
  return new Set(
    messages.filter((message) => message.unread || message === newest).map(({ id }) => id),
  );
}

// foldRows returns the rows that show messages, oldest first, given the
// IDs of those that keep their own row, e.g. open ones. The first message
// and the newest keptNewest always show; between them, each run of at
// least minHidden other messages becomes one "N more messages" row while
// folding is "grouped".
export function foldRows<M extends Foldable>(
  messages: readonly M[],
  shown: ReadonlySet<string>,
  folding: Folding,
): FoldedRow<M>[] {
  const rows: FoldedRow<M>[] = [];
  let run: M[] = [];
  const endRun = () => {
    if (folding === "grouped" && run.length >= minHidden)
      rows.push({ kind: "more", messages: run });
    else for (const message of run) rows.push({ kind: "message", message });
    run = [];
  };
  messages.forEach((message, i) => {
    const between = i > 0 && i < messages.length - keptNewest;
    if (between && !shown.has(message.id)) {
      run.push(message);
      return;
    }
    endRun();
    rows.push({ kind: "message", message });
  });
  endRun();
  return rows;
}
