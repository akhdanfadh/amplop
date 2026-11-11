// What pollWhile reads of a query: TanStack Query passes the whole query to
// refetchInterval.
type PolledQuery<T> = { state: { status: "pending" | "error" | "success"; data: T | undefined } };

// pollWhile returns a refetchInterval that reads again every ms while busy
// says the data still changes by itself. A failed read stops it, though the
// data kept from before may look busy: repeating it would only repeat the
// error, so the page's Try again restarts it.
export function pollWhile<T>(
  ms: number,
  busy: (data: T) => boolean,
): (query: PolledQuery<T>) => number | false {
  return ({ state }) => {
    if (state.status === "error" || state.data === undefined) return false;
    return busy(state.data) ? ms : false;
  };
}
