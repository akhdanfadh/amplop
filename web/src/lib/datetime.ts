// How inbok writes a time or a day: in its own British English words, as
// mail's dates are ("14 Nov 2026, 6:18 pm"), on the browser's 12 or 24 hour
// clock and in its time zone (DESIGN.md "Type").

// The browser's 12 or 24 hour clock.
export const hourCycle = new Intl.DateTimeFormat(undefined, {
  hour: "numeric",
}).resolvedOptions().hourCycle;

// clockTime writes a time of day, e.g. "2:32 pm", or "14:32" on a 24 hour
// clock.
export const clockTime = new Intl.DateTimeFormat("en-GB", {
  hour: "numeric",
  minute: "2-digit",
  hourCycle,
});

// dayDate writes a day, e.g. "14 Nov 2026".
export const dayDate = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  year: "numeric",
});

// dayAndTime writes a day and its time, e.g. "14 Nov 2026, 2:32 pm".
export const dayAndTime = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  year: "numeric",
  hour: "numeric",
  minute: "2-digit",
  hourCycle,
});
