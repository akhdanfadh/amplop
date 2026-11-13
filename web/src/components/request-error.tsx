import type { ReactElement, ReactNode } from "react";
import { FieldError } from "@/components/ui/field";
import { cn } from "@/lib/cn";

// RequestError shows why a form's request failed, under its button: errors
// as an alert, then children, e.g. a button to check the outcome, then the
// request reference when there are errors and requestId is given. It renders
// nothing when it has neither errors nor children.
export function RequestError({
  errors,
  requestId,
  className,
  children,
}: {
  errors: readonly { message: string }[];
  requestId?: string;
  className?: string;
  children?: ReactNode;
}): ReactElement | null {
  if (errors.length === 0 && !children) return null;

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {/* Unlike a field's error, which is read with its field, nothing
          points at this one, so it is announced as it appears. */}
      <FieldError role="alert" errors={errors} />
      {children}
      {/* No errors means the fields show the whole refusal; a reference is
          for reporting a fault, which a refused value is not. */}
      {errors.length > 0 && requestId && (
        <p className="text-sm text-muted-foreground">
          Request reference: <code className="break-all">{requestId}</code>
        </p>
      )}
    </div>
  );
}
