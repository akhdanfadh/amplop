import * as z from "zod";
import { ConnectOutcome } from "@/api/generated/model";

// connectSearch reads the Connected accounts page's address, where Google's
// return to amplop lands with how the connection ended. A value that is not a
// ConnectOutcome is dropped, so it shows no message. A valid one typed by
// hand shows its fixed text above the list the server reads, and changes
// nothing.
export const connectSearch = z.object({ outcome: ConnectOutcome.optional().catch(undefined) });
