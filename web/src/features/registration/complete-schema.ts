import * as z from "zod";
import { RedeemRegistrationLinkBody } from "@/api/generated/model";

// CompleteSchema checks a new member's password and its confirmation. The
// secret comes from the link, not the form.
export const CompleteSchema = RedeemRegistrationLinkBody.pick({ password: true })
  .extend({
    confirmPassword: z.string().min(1, { error: "Confirm your password." }),
  })
  .refine((value) => value.password === value.confirmPassword, {
    path: ["confirmPassword"],
    error: "Passwords must match.",
  });
