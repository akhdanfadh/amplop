import * as z from "zod";
import {
  PasswordProof,
  passwordProofPasswordMax,
  TotpDisableCode,
  TotpReplacementCode,
  TotpEnrollmentCode,
} from "@/api/generated/model";
import { completeCode, type CodeKind } from "@/features/auth/code-schema";

// EnrollmentCodeSchema is the first code from the authenticator being set up.
export const EnrollmentCodeSchema = TotpEnrollmentCode.extend({
  code: completeCode(TotpEnrollmentCode.shape.code, "authenticator"),
});

// ReplacementCodeSchema is a fresh authenticator code; a backup code
// cannot replace the backup codes.
export const ReplacementCodeSchema = TotpReplacementCode.extend({
  code: completeCode(TotpReplacementCode.shape.code, "authenticator"),
});

// PasswordSchema is the account's current password, asked before the
// enrollment's secret or its waiting backup codes show.
export const PasswordSchema = PasswordProof.extend({
  password: z
    .string()
    .min(1, { error: "Enter your password." })
    .max(passwordProofPasswordMax, {
      error: `Use at most ${passwordProofPasswordMax} characters.`,
    }),
});

// turnOffSchema takes either proof, so the field follows the selected kind.
export function turnOffSchema(kind: CodeKind) {
  return TotpDisableCode.extend({ code: completeCode(TotpDisableCode.shape.code, kind) });
}
