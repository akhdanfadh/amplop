import * as z from "zod";
import { NewSession, newSessionPasswordMax } from "@/api/generated/model";

// SignInSchema checks credentials without applying account-creation password rules.
export const SignInSchema = NewSession.extend({
  password: z
    .string()
    .min(1, { error: "Enter your password." })
    .max(newSessionPasswordMax, {
      error: `Use at most ${newSessionPasswordMax} characters.`,
    }),
});
