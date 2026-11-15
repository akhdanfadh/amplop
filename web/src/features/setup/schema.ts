import * as z from "zod";
import { NewSetup } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";

export const SetupSchema = NewSetup.extend({
  email: emailAddress(NewSetup.shape.email),
  token: NewSetup.shape.token.min(1, { error: "Enter the setup token." }),
  confirmPassword: z.string().min(1, { error: "Confirm your password." }),
}).refine((value) => value.password === value.confirmPassword, {
  path: ["confirmPassword"],
  error: "Passwords must match.",
});
