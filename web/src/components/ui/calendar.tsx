import * as React from "react";
import { cn } from "@/lib/cn";
import {
  Calendar as AriaCalendar,
  CalendarCell,
  CalendarGrid,
  CalendarGridBody,
  CalendarGridHeader,
  CalendarHeaderCell,
  CalendarMonthPicker,
  CalendarYearPicker,
  type CalendarCellRenderProps,
  type CalendarProps,
  type DateValue,
} from "react-aria-components";
import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

// Calendar picks one day on a month's grid, its month and year chosen from
// menus above it, so a day years back is two choices away.
//
// DESIGN: amplop's copy differs from shadcn's as its other components do: one
// month, single selection only, as its one caller needs; the month and year
// are always menus; cells take DESIGN.md's focus ring and touch size; it
// fills its column's width, each day as wide as a seventh of it, so it lines
// up with fields above it and a range's shading runs unbroken; dayClassName
// lets a caller mark days the selection alone cannot, e.g. a range's other
// end.
function Calendar<T extends DateValue>({
  className,
  dayClassName,
  ...props
}: Omit<CalendarProps<T>, "visibleDuration" | "selectionMode"> & {
  dayClassName?: (date: DateValue, state: CalendarCellRenderProps) => string | undefined;
}) {
  return (
    <AriaCalendar
      selectionMode="single"
      data-slot="calendar"
      className={cn("flex w-full flex-col gap-2", className)}
      {...props}
    >
      <header className="flex items-center gap-1">
        <Button variant="ghost" size="icon-sm" slot="previous" aria-label="Previous month">
          <ChevronLeftIcon aria-hidden="true" />
        </Button>
        <CalendarMonthPicker>
          {(picker) => (
            <Select {...picker} className="flex-1">
              <SelectTrigger size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="max-h-72 min-w-0">
                {picker.items.map((item) => (
                  <SelectItem key={item.id} id={item.id}>
                    {item.formatted}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </CalendarMonthPicker>
        <CalendarYearPicker>
          {(picker) => (
            <Select {...picker} className="flex-1">
              <SelectTrigger size="sm" className="tabular-nums">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="max-h-72 min-w-0">
                {picker.items.map((item) => (
                  <SelectItem key={item.id} id={item.id} className="tabular-nums">
                    {item.formatted}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </CalendarYearPicker>
        <Button variant="ghost" size="icon-sm" slot="next" aria-label="Next month">
          <ChevronRightIcon aria-hidden="true" />
        </Button>
      </header>
      <CalendarGrid className="w-full table-fixed border-separate border-spacing-y-0.5">
        <CalendarGridHeader>
          {(day) => (
            <CalendarHeaderCell className="pb-1 text-xs font-normal text-muted-foreground">
              {day}
            </CalendarHeaderCell>
          )}
        </CalendarGridHeader>
        <CalendarGridBody>
          {(date) => (
            <CalendarCell
              date={date}
              className={(state) =>
                cn(
                  "grid h-9 w-full cursor-default place-items-center rounded-md text-sm tabular-nums transition-colors outline-none data-focus-visible:focus-ring-inset data-hovered:bg-accent data-outside-month:invisible data-unavailable:text-muted-foreground pointer-coarse:h-11 data-disabled:text-muted-foreground",
                  "data-selected:bg-primary data-selected:text-primary-foreground data-selected:data-hovered:bg-primary-hover",
                  dayClassName?.(date, state),
                )
              }
            />
          )}
        </CalendarGridBody>
      </CalendarGrid>
    </AriaCalendar>
  );
}

export { Calendar };
