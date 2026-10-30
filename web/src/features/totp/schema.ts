import { REGEXP_ONLY_DIGITS } from "input-otp";
import {
  TotpConfirmationCode,
  TotpDisableCode,
  totpConfirmationCodeCodeMax,
  TotpRegenerationCode,
  totpRegenerationCodeCodeMax,
} from "@/api/generated/model";
import { completeCode, type CodeKind } from "@/features/auth/code-schema";

const authenticatorDigits = totpConfirmationCodeCodeMax;
export const ConfirmationSchema = TotpConfirmationCode.extend({
  code: TotpConfirmationCode.shape.code
    .length(authenticatorDigits, { error: `Enter all ${authenticatorDigits} digits.` })
    .regex(new RegExp(REGEXP_ONLY_DIGITS), { error: "Use digits only." }),
});

export const RegenerationSchema = TotpRegenerationCode.extend({
  code: TotpRegenerationCode.shape.code
    .length(totpRegenerationCodeCodeMax, {
      error: `Enter all ${totpRegenerationCodeCodeMax} digits.`,
    })
    .regex(new RegExp(REGEXP_ONLY_DIGITS), { error: "Use digits only." }),
});

// Turning off accepts either proof, so the field follows the selected kind.
export function turnOffSchema(kind: CodeKind) {
  return TotpDisableCode.extend({ code: completeCode(TotpDisableCode.shape.code, kind) });
}
