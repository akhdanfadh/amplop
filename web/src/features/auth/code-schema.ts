import { NewCodeSession } from "@/api/generated/model";
import { REGEXP_ONLY_DIGITS, REGEXP_ONLY_DIGITS_AND_CHARS } from "input-otp";

export const codeFormats = {
  authenticator: { length: 6, pattern: REGEXP_ONLY_DIGITS, label: "Authenticator code" },
  backup: { length: 10, pattern: REGEXP_ONLY_DIGITS_AND_CHARS, label: "Backup code" },
} as const;

export type CodeKind = keyof typeof codeFormats;

// Reject incomplete entries locally; the server checks whether the proof is valid.
export function codeSchema(kind: CodeKind) {
  const { length, pattern } = codeFormats[kind];
  const message = kind === "backup" ? "Enter all 10 characters." : "Enter all 6 digits.";
  return NewCodeSession.extend({
    code: NewCodeSession.shape.code.length(length, { error: message }).regex(new RegExp(pattern), {
      error: message,
    }),
  });
}

// A displayed separator has no slot. Match the server's accepted paste formatting.
export function codePaste(value: string): string {
  return value.replace(/[\p{White_Space}-]/gu, "");
}
