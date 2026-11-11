import type { ReactElement } from "react";
import type { ApiError } from "@/api/transport";
import { Button } from "@/components/ui/button";

const buttonClass =
  "h-auto min-h-11 w-fit border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";

// ReadError says that a read the page needs failed, with message, the
// error's request reference, and Try again, which runs onRetry and is
// disabled while retrying.
export function ReadError({
  message,
  error,
  retrying,
  onRetry,
}: {
  message: string;
  error: ApiError;
  retrying: boolean;
  onRetry: () => void;
}): ReactElement {
  return (
    <div className="flex flex-col gap-3" role="alert">
      <p>{message}</p>
      {error.requestId && (
        <p className="text-sm text-muted-foreground">
          Request reference: <code className="break-all">{error.requestId}</code>
        </p>
      )}
      <Button
        type="button"
        variant="outline"
        className={buttonClass}
        disabled={retrying}
        onClick={onRetry}
      >
        Try again
      </Button>
    </div>
  );
}
