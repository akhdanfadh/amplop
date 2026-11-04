import * as z from "zod";
import { RedeemPasswordResetLinkBody } from "@/api/generated/model";

// ResetSchema checks the email the visitor types, the new password, and its
// confirmation. The secret comes from the link, not the form; the server
// checks that the email is the link's.
export const ResetSchema = RedeemPasswordResetLinkBody.pick({ email: true, password: true })
  .extend({
    confirmPassword: z.string().min(1, { error: "Confirm your new password." }),
  })
  .refine((value) => value.password === value.confirmPassword, {
    path: ["confirmPassword"],
    error: "Passwords must match.",
  });
