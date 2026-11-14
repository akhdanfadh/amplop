// How a list row shows its date (DESIGN.md "Message list"): today the
// time, on the browser's 12 or 24 hour clock; "Yesterday"; the weekday
// within a week; "2 Nov" this year; "2 Nov 2025" before.
import { useEffect, useState } from "react";
import { yesterday } from "./text";

const day = { year: "numeric", month: "numeric", day: "numeric" } as const;
// The browser's own clock style; inbok's words are English.
const time = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
const weekday = new Intl.DateTimeFormat("en-US", { weekday: "short" });
const dayMonth = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });
const dayMonthYear = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  year: "numeric",
});
// A message header's date, e.g. "14 Nov 2026, 6:18 pm": the day in
// inbok's words, the time on the browser's clock.
const medium = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  year: "numeric",
  hour: "numeric",
  minute: "2-digit",
  hourCycle: time.resolvedOptions().hourCycle,
});
// The day in inbok's words, the time on the browser's clock, as rows show it.
const full = new Intl.DateTimeFormat("en-GB", {
  dateStyle: "full",
  timeStyle: "short",
  hourCycle: time.resolvedOptions().hourCycle,
});
const sameDay = new Intl.DateTimeFormat("en-US", day);

// Days before now's day that still show a weekday.
const weekdayDays = 6;

// listDate returns date as a list row shows it, seen at now in the
// browser's time zone.
export function listDate(date: Date, now: Date): string {
  const days = daysBetween(date, now);
  if (days === 0) return time.format(date);
  if (days === 1) return yesterday;
  // A later day, e.g. from a sender whose clock runs ahead, shows its date.
  if (days > 0 && days <= weekdayDays) return weekday.format(date);
  if (date.getFullYear() === now.getFullYear()) return dayMonth.format(date);
  return dayMonthYear.format(date);
}

// headerDate returns date's day and time as a message's header shows them,
// e.g. "14 Nov 2026, 6:18 pm".
export function headerDate(date: Date): string {
  return medium.format(date);
}

// fullDate returns date's day and time in full, e.g. for hovering a row's
// date.
export function fullDate(date: Date): string {
  return full.format(date);
}

// daysBetween returns how many calendar days date's day is before now's,
// in the browser's time zone; negative for a later day.
function daysBetween(date: Date, now: Date): number {
  if (sameDay.format(date) === sameDay.format(now)) return 0;
  // Round: a day with a daylight saving change is 23 or 25 hours long.
  return Math.round((midnight(now) - midnight(date)) / 86_400_000);
}

// midnight returns when d's day began, in the browser's time zone.
function midnight(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

// useToday returns the time now, moving on only when the day changes:
// that is all a row's date depends on ("Yesterday", a weekday), so rows
// draw again once a day, not every minute.
export function useToday(): Date {
  const [today, setToday] = useState(() => new Date());
  useEffect(() => {
    const next = new Date(today);
    next.setHours(24, 0, 1, 0);
    const timer = setTimeout(() => setToday(new Date()), next.getTime() - Date.now());
    return () => clearTimeout(timer);
  }, [today]);
  return today;
}
