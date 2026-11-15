import { REGEXP_ONLY_DIGITS } from "input-otp";
import {
  TotpConfirmationCode,
  TotpDisableCode,
  totpConfirmationCodeCodeMax,
  TotpReplacementCode,
} from "@/api/generated/model";
import { completeCode, type CodeKind } from "@/features/auth/code-schema";

const authenticatorDigits = totpConfirmationCodeCodeMax;
export const ConfirmationSchema = TotpConfirmationCode.extend({
  code: TotpConfirmationCode.shape.code
    .length(authenticatorDigits, { error: `Enter all ${authenticatorDigits} digits.` })
    .regex(new RegExp(REGEXP_ONLY_DIGITS), { error: "Use digits only." }),
});

// ReplacementCodeSchema is a fresh authenticator code; a backup code
// cannot replace the backup codes.
export const ReplacementCodeSchema = TotpReplacementCode.extend({
  code: completeCode(TotpReplacementCode.shape.code, "authenticator"),
});

// turnOffSchema takes either proof, so the field follows the selected kind.
export function turnOffSchema(kind: CodeKind) {
  return TotpDisableCode.extend({ code: completeCode(TotpDisableCode.shape.code, kind) });
}
