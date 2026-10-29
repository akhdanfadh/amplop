import { REGEXP_ONLY_DIGITS } from "input-otp";
import { TotpConfirmationCode, totpConfirmationCodeCodeMax } from "@/api/generated/model";

export const authenticatorDigits = totpConfirmationCodeCodeMax;
export const ConfirmationSchema = TotpConfirmationCode.extend({
  code: TotpConfirmationCode.shape.code
    .length(authenticatorDigits, { error: `Enter all ${authenticatorDigits} digits.` })
    .regex(new RegExp(REGEXP_ONLY_DIGITS), { error: "Use digits only." }),
});

export function pasteCode(value: string): string {
  return value.replace(/[\p{White_Space}-]/gu, "");
}
