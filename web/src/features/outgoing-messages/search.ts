import * as z from "zod";

// composeSearch reads the compose page's address: edit names a message
// whose content the form starts with, e.g. one Gmail refused. Any other
// value is dropped, so the form starts empty.
export const composeSearch = z.object({ edit: z.uuid().optional().catch(undefined) });
