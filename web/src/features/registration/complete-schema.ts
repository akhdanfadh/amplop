import * as z from "zod";
import { RedeemRegistrationLinkBody } from "@/api/generated/model";

// CompleteSchema checks the email the visitor types, a new member's
// password, and its confirmation. The secret comes from the link, not the
// form; the server checks that the email is the link's.
export const CompleteSchema = RedeemRegistrationLinkBody.pick({ email: true, password: true })
  .extend({
    confirmPassword: z.string().min(1, { error: "Confirm your password." }),
  })
  .refine((value) => value.password === value.confirmPassword, {
    path: ["confirmPassword"],
    error: "Passwords must match.",
  });
