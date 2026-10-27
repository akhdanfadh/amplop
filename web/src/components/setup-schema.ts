import * as z from "zod";
import { NewSetup, newSetupPasswordMin, newSetupPasswordMax } from "@/api/generated/model";

// NOTE: Accept Zod's stricter email syntax even though Go's mail parser allows more.
export const SetupSchema = NewSetup.extend({
  token: NewSetup.shape.token.min(1, { error: "Enter the setup token." }),
  password: characterRange(newSetupPasswordMin, newSetupPasswordMax),
  confirmPassword: z.string().min(1, { error: "Confirm your password." }),
}).refine((value) => value.password === value.confirmPassword, {
  path: ["confirmPassword"],
  error: "Passwords must match.",
});

// Count Unicode code points like Go.
function characterRange(min: number, max: number) {
  return z.string().refine(
    (value) => {
      const length = Array.from(value).length;
      return length >= min && length <= max;
    },
    { error: `Must be ${min} to ${max} characters.` },
  );
}
