import { useContext, useEffect, useId, useState, type ReactElement } from "react";
import {
  Button as AriaButton,
  DateField,
  DateFieldStateContext,
  DateInput,
  DateSegment,
  I18nProvider,
  Label,
  type DateValue,
} from "react-aria-components";
import {
  getLocalTimeZone,
  parseDate,
  toCalendarDate,
  today,
  type CalendarDate,
} from "@internationalized/date";
import { CheckIcon, XIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import type { DatePreset, FilterChange, SearchFilters } from "./search";
import {
  anyTimeName,
  applyName,
  beforeName,
  clearFieldName,
  datePresetNames,
  pickDayName,
  rangeBackwards,
  rangeFuture,
  rangePartly,
  rangeHelp,
  sinceName,
} from "./text";

// The ends of a chosen range.
type End = "since" | "before";

// DateChooser picks a search's date: a choice relative to today, which
// applies at once, or a range of days, either end open, typed or picked on
// the calendar, which Apply sets. The field last focused is the one a day
// picked fills; filling one moves to the other while it is empty, and
// typing a date moves the calendar to it, so a year away is one
// keystroke. No day after today can be picked. onChange gets the change.
export function DateChooser({
  filters,
  onChange,
}: {
  filters: SearchFilters;
  onChange: (change: FilterChange) => void;
}): ReactElement {
  const now = today(getLocalTimeZone());
  const [since, setSince] = useState<CalendarDate | null>(day(filters.since));
  const [before, setBefore] = useState<CalendarDate | null>(day(filters.before));
  const [active, setActive] = useState<End>(since !== null && before === null ? "before" : "since");
  const [focused, setFocused] = useState<DateValue>(since ?? before ?? now);
  const backwards = since !== null && before !== null && before.compare(since) <= 0;
  // A typed day after today, which the calendar cannot pick either.
  const future = [since, before].some((end) => end !== null && end.compare(now) > 0);
  // Which fields hold a day only partly typed, e.g. its year still empty:
  // the field then still holds its last whole day, which Apply must not
  // send.
  const [partly, setPartly] = useState<Record<End, boolean>>({ since: false, before: false });
  const help = useId();
  const unfinished = partly.since || partly.before;

  const set = (end: End, value: CalendarDate | null) => {
    if (end === "since") setSince(value);
    else setBefore(value);
    if (value) setFocused(value);
  };
  // The other end next, while it is still open.
  const moveOn = (end: End) => {
    if (end === "since" && before === null) setActive("before");
    else if (end === "before" && since === null) setActive("since");
  };
  const preset = (date: DatePreset | undefined) =>
    onChange({ date, since: undefined, before: undefined });
  const apply = () =>
    onChange({ date: undefined, since: since?.toString(), before: before?.toString() });

  const field = (end: End, label: string, value: CalendarDate | null) => (
    <div onFocusCapture={() => setActive(end)} className="min-w-0 flex-1">
      <DateField
        value={value}
        maxValue={now}
        aria-describedby={help}
        granularity="day"
        onChange={(next) => {
          set(end, next && toCalendarDate(next));
          if (next) moveOn(end);
        }}
        className="flex flex-col gap-1"
      >
        <Label className="text-sm font-medium">{label}</Label>
        <PartlyTyped
          onChange={(partial) =>
            setPartly((was) => (was[end] === partial ? was : { ...was, [end]: partial }))
          }
        />
        <div
          className={cn(
            "flex h-9 items-center rounded-lg border bg-card pr-1 pl-2 transition-colors dark:bg-input/10 pointer-coarse:h-11",
            // The end a picked day fills; a day after today is refused.
            active === end ? "border-primary ring-1 ring-primary" : "border-input",
            "in-data-invalid:border-destructive in-data-invalid:ring-destructive",
          )}
        >
          <DateInput className="flex flex-1 text-sm tabular-nums">
            {(segment) => (
              <DateSegment
                segment={segment}
                className="rounded-xs px-0.5 outline-none data-focused:bg-primary data-focused:text-primary-foreground data-placeholder:text-muted-foreground data-[type=literal]:px-0"
              />
            )}
          </DateInput>
          {value !== null && (
            <AriaButton
              aria-label={clearFieldName(label)}
              onPress={() => set(end, null)}
              // 36 px on touch, its target reaching 4 px past it: 44 px,
              // within the field's own height.
              className="relative grid size-7 shrink-0 cursor-default place-items-center rounded-md text-muted-foreground transition-colors outline-none data-focus-visible:focus-ring data-hovered:bg-accent data-hovered:text-foreground pointer-coarse:size-9 pointer-coarse:after:absolute pointer-coarse:after:-inset-1"
            >
              <XIcon aria-hidden="true" className="size-4" />
            </AriaButton>
          )}
        </div>
      </DateField>
    </div>
  );

  const chosen = filters.date ?? (filters.since || filters.before ? null : "any");
  return (
    // Days in inbok's British English, as every date it writes: 14/11/2026,
    // months named as "14 Nov 2026" names them, weeks from Monday.
    <I18nProvider locale="en-GB">
      <div className="flex max-w-[calc(100vw-2rem)] flex-col gap-3 p-1 sm:flex-row">
        <div className="flex flex-col gap-0.5 sm:w-40 sm:shrink-0 sm:border-r sm:pr-1">
          {(["any", "7d", "30d", "1y", "older"] as const).map((key) => (
            <AriaButton
              key={key}
              aria-pressed={chosen === key}
              onPress={() => preset(key === "any" ? undefined : key)}
              className="flex min-h-8 cursor-default items-center justify-between gap-2 rounded-md px-2 text-left text-sm transition-colors outline-none data-focus-visible:focus-ring-inset data-hovered:bg-accent pointer-coarse:min-h-11"
            >
              {key === "any" ? anyTimeName : datePresetNames[key]}
              {chosen === key && <CheckIcon aria-hidden="true" className="size-4" />}
            </AriaButton>
          ))}
        </div>
        {/* Seven 40 px days wide, 44 px on touch; the fields above share
            its width, so their edges line up with the calendar's. */}
        <div className="flex w-72 max-w-full flex-col gap-2 pointer-coarse:w-80">
          <div className="flex gap-2">
            {field("since", sinceName, since)}
            {field("before", beforeName, before)}
          </div>
          <Calendar
            aria-label={pickDayName(active === "since" ? sinceName : beforeName)}
            // The fields hold the chosen days, and the marks below show
            // them: a selected day would fill the Before day too.
            value={null}
            focusedValue={focused}
            onFocusChange={setFocused}
            maxValue={now}
            onChange={(picked) => {
              set(active, toCalendarDate(picked));
              moveOn(active);
            }}
            dayClassName={(date) => {
              const isSince = since !== null && date.compare(since) === 0;
              const isBefore = before !== null && date.compare(before) === 0;
              const inside =
                since !== null &&
                before !== null &&
                date.compare(since) > 0 &&
                date.compare(before) < 0;
              return cn(
                inside && "rounded-none bg-accent",
                isSince && "bg-primary text-primary-foreground data-hovered:bg-primary-hover",
                // Before is outlined, not filled: the range stops short of it.
                isBefore && "ring-2 ring-primary ring-inset",
              );
            }}
          />
          <div className="flex items-center justify-between gap-2">
            <p
              id={help}
              aria-live="polite"
              className={cn("text-xs", future ? "text-destructive" : "text-muted-foreground")}
            >
              {unfinished
                ? rangePartly
                : future
                  ? rangeFuture
                  : backwards
                    ? rangeBackwards
                    : rangeHelp}
            </p>
            <Button size="sm" isDisabled={backwards || future || unfinished} onPress={apply}>
              {applyName}
            </Button>
          </div>
        </div>
      </div>
    </I18nProvider>
  );
}

// PartlyTyped tells onChange, inside a DateField, whether its day is only
// partly typed: some of its parts filled, some not. React Aria reports a
// day only once every part is filled, keeping the last whole one until
// then.
function PartlyTyped({ onChange }: { onChange: (partial: boolean) => void }): null {
  const state = useContext(DateFieldStateContext);
  const parts = state?.segments.filter((segment) => segment.isEditable) ?? [];
  const partial =
    parts.some((segment) => segment.isPlaceholder) &&
    parts.some((segment) => !segment.isPlaceholder);
  useEffect(() => onChange(partial), [partial, onChange]);
  return null;
}

// day returns value, a YYYY-MM-DD date from the address, as a calendar day,
// or null for none.
function day(value: string | undefined): CalendarDate | null {
  return value ? parseDate(value) : null;
}
