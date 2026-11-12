import type { ReactElement } from "react";
import type { ApiError } from "@/api/transport";
import { Button } from "@/components/ui/button";

const buttonClass = "w-fit whitespace-normal";

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
        isDisabled={retrying}
        onPress={onRetry}
      >
        Try again
      </Button>
    </div>
  );
}
