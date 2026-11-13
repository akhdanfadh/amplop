// The views the sidebar lists, in its order (DESIGN.md "Sidebar"); each
// is a place in the address, /mail/<place>. Labels join them as places
// when mail is built.
export const viewPlaces = ["inbox", "starred", "sent", "all", "spam", "trash"] as const;
export type ViewPlace = (typeof viewPlaces)[number];

// isViewPlace reports whether place, from the address, names a view.
export function isViewPlace(place: string): place is ViewPlace {
  return (viewPlaces as readonly string[]).includes(place);
}
