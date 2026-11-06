import * as z from "zod";
import { NewSetup } from "@/api/generated/model";

// DESIGN: Accept Zod's stricter email syntax even though Go's mail parser
// allows more.
export const SetupSchema = NewSetup.extend({
  token: NewSetup.shape.token.min(1, { error: "Enter the setup token." }),
  confirmPassword: z.string().min(1, { error: "Confirm your password." }),
}).refine((value) => value.password === value.confirmPassword, {
  path: ["confirmPassword"],
  error: "Passwords must match.",
});
