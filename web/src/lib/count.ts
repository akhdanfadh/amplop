// Counts take a comma, e.g. 1,284 (DESIGN.md "Message list"), in the
// app's one English locale.
const counts = new Intl.NumberFormat("en-US");

// formatCount returns count as the app shows a number of things, e.g.
// unread conversations: "12,847".
export function formatCount(count: number): string {
  return counts.format(count);
}
