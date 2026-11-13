import * as z from "zod";
import { NewSession, newSessionPasswordMax } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";

// SignInSchema checks credentials without applying account-creation password rules.
export const SignInSchema = NewSession.extend({
  email: emailAddress(NewSession.shape.email),
  password: z
    .string()
    .min(1, { error: "Enter your password." })
    .max(newSessionPasswordMax, {
      error: `Use at most ${newSessionPasswordMax} characters.`,
    }),
});
