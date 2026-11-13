import * as z from "zod";

// A path on this site: one slash, then not a second slash or a backslash,
// which browsers read as the start of another host (//evil.example).
const sitePath = /^\/(?![/\\])/;

// signInSearch reads the sign-in page's address: redirect is where a
// member who was sent to sign in goes back to afterwards. Anything but a
// path on this site is dropped, so a crafted link can't send a member
// elsewhere after they sign in.
export const signInSearch = z.object({
  redirect: z.string().regex(sitePath).optional().catch(undefined),
});
