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
// each rowSize pixels tall, in its own scrolling box. Props go to React
// Aria's GridList.
//
// DESIGN: type-to-find is off: a row's text is a whole conversation, and
// letter keys are left for app keyboard shortcuts (ADR031).
function GridList<T extends object>({
  rowSize,
  className,
  ...props
}: Omit<AriaGridListProps<T>, "className" | "disallowTypeAhead"> & {
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
        // contain: scrolling past the end never moves the page, nor starts
        // a phone browser's own pull-to-refresh.
        className={cn("h-full overflow-auto overscroll-y-contain outline-none", className)}
        {...props}
      />
    </Virtualizer>
  );
}

// GridListItem is one row: hovered, it has the accent surface; focused by
// the keyboard, the ring inside its edge, which the list's box would clip
// outside.
function GridListItem<T extends object>({
  className,
  ...props
}: Omit<AriaGridListItemProps<T>, "className"> & { className?: string }) {
  return (
    <AriaGridListItem
      data-slot="grid-list-item"
      className={cn(
        "relative flex cursor-default items-stretch transition-colors outline-none select-none data-focus-visible:focus-ring-inset data-hovered:bg-accent",
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
