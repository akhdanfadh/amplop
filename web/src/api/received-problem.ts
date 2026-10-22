import { isUriReference } from "@hyperjump/uri";
import * as z from "zod";

const minHttpStatus = 100;
const maxHttpStatus = 599;

// Hyperjump throws for unsupported IP versions; ignore that member, not the body.
function uriReference(value: string) {
  try {
    return isUriReference(value);
  } catch {
    return false;
  }
}

// RFC 9457 standard members are optional; incorrectly typed members are ignored.
// This is deliberately separate from the stricter generated Inbok output schema.
export const ReceivedProblem = z.object({
  type: z.string().refine(uriReference).optional().catch(undefined),
  status: z.int().min(minHttpStatus).max(maxHttpStatus).optional().catch(undefined),
  title: z.string().optional().catch(undefined),
  detail: z.string().optional().catch(undefined),
  instance: z.string().refine(uriReference).optional().catch(undefined),
  errors: z
    .array(z.object({ location: z.string(), message: z.string() }))
    .min(1)
    .optional()
    .catch(undefined),
});

export type ReceivedProblem = z.output<typeof ReceivedProblem>;
