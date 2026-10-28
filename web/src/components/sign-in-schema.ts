import * as z from "zod";
import { NewSession, newSessionPasswordMax } from "@/api/generated/model";

// SignInSchema checks credentials without applying account-creation password rules.
// Count password code points like Go; the generated string limit counts UTF-16 units.
export const SignInSchema = NewSession.extend({
  password: z
    .string()
    .min(1, { error: "Enter your password." })
    .refine((value) => Array.from(value).length <= newSessionPasswordMax, {
      error: `Use at most ${newSessionPasswordMax} characters.`,
    }),
});
