import * as z from "zod";
import { isViewPlace, type Place } from "./places";

// placeSearch reads a mail address's search: account, the one connected
// account a list shows, e.g. /mail/inbox?account=3. It only filters, so an
// account that is no ID, e.g. ?account=abc, is dropped and the list shows
// every account's, as an old link should (web/README.md "Routes").
export const placeSearch = z.object({ account: z.int().positive().optional().catch(undefined) });

// A label's ID, as the API names one.
const labelId = z.uuid();

// isPlace reports whether place, from the address, names a view or could
// name a label; whether that label exists is the API's to say.
export function isPlace(place: string): place is Place {
  return isViewPlace(place) || labelId.safeParse(place).success;
}

// A conversation's ID, which the client puts in the API's path as is:
// anything else, e.g. ../ segments, would reach another path of the API.
export const conversationIdSchema = z.uuid();
