import { useMemo } from "react";
import { cn } from "@/lib/cn";
import {
  GridList as AriaGridList,
  GridListItem as AriaGridListItem,
  GridListLoadMoreItem as AriaGridListLoadMoreItem,
  ListLayout,
  Virtualizer,
  type GridListItemProps as AriaGridListItemProps,
  type GridListLoadMoreItemProps,
  type GridListProps as AriaGridListProps,
} from "react-aria-components";

// GridList is the message list (DESIGN.md "Components"): React Aria's
// GridList inside its Virtualizer, so only the rows on screen are drawn,
// each rowSize pixels tall, in its own scrolling box. Rows navigate
// through their href. Props go to React Aria's GridList.
//
// DESIGN: type-to-find is off: a row's text is a whole conversation, and
// letter keys are left for app keyboard shortcuts (ADR031). Selection
// toggles, so a click opens a row until rows are picked
// (https://react-aria.adobe.com/selection#selection-behavior).
function GridList<T extends object>({
  rowSize,
  className,
  ...props
}: Omit<AriaGridListProps<T>, "className" | "disallowTypeAhead" | "selectionBehavior"> & {
  rowSize: number;
  className?: string;
}) {
  // One object while rowSize stays: a new one has the Virtualizer lay the
  // list out again.
  const layoutOptions = useMemo(() => ({ rowSize, loaderSize: rowSize }), [rowSize]);
  return (
    <Virtualizer layout={ListLayout} layoutOptions={layoutOptions}>
      <AriaGridList
        data-slot="grid-list"
        disallowTypeAhead
        selectionBehavior="toggle"
        // contain: scrolling past the end never moves the page, nor starts
        // a phone browser's own pull-to-refresh.
        className={cn("h-full overflow-auto overscroll-y-contain outline-none", className)}
        {...props}
      />
    </Virtualizer>
  );
}

// GridListItem is one row: href opens it; hovered, it has the accent
// surface; current, the row the page shows, e.g. the open conversation,
// the selection surface, hovered or not, and aria-current="page"; focused
// by the keyboard, the ring inside its edge, which the list's box would
// clip outside.
//
// DESIGN: React Aria passes no aria-current through, so the row's own div
// adds it, through the render prop React Aria documents for that.
function GridListItem<T extends object>({
  className,
  current = false,
  ...props
}: Omit<AriaGridListItemProps<T>, "className" | "render"> & {
  className?: string;
  current?: boolean;
}) {
  return (
    <AriaGridListItem
      data-slot="grid-list-item"
      render={(domProps) => <div {...domProps} aria-current={current ? "page" : undefined} />}
      className={cn(
        "relative flex cursor-default items-stretch transition-colors outline-none select-none aria-[current=page]:bg-selection data-focus-visible:focus-ring-inset data-hovered:not-aria-[current=page]:bg-accent",
        className,
      )}
      {...props}
    />
  );
}

// GridListLoadMore asks for more rows, by onLoadMore, as the member
// scrolls near the end, and shows children while isLoading.
function GridListLoadMore({ className, ...props }: GridListLoadMoreItemProps) {
  return (
    <AriaGridListLoadMoreItem
      data-slot="grid-list-load-more"
      className={cn("flex items-center", className)}
      {...props}
    />
  );
}

// GridListEnd closes a list that has nothing more to load with children,
// e.g. where the copied mail ends.
//
// DESIGN: it is React Aria's load-more row, shown: the only row a
// virtualized GridList draws that is not an item, so the keyboard skips
// it, it adds nothing to the row count, and it scrolls with the rows.
function GridListEnd({ className, children }: { className?: string; children: string }) {
  return (
    <AriaGridListLoadMoreItem
      data-slot="grid-list-end"
      isLoading
      className={cn("flex items-center px-4 text-sm text-muted-foreground", className)}
    >
      {children}
    </AriaGridListLoadMoreItem>
  );
}

export { GridList, GridListEnd, GridListItem, GridListLoadMore };
