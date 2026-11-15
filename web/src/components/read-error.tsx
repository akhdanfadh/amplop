import type { ReactElement } from "react";
import type { ApiError } from "@/api/transport";
import { Button } from "@/components/ui/button";

const buttonClass = "w-fit whitespace-normal";

// ReadError says that a read the page needs failed, with message, the
// error's request reference when the API answered, and Try again, which
// runs onRetry and is disabled while retrying. placement says where it
// shows: "in-page", the default, as an alert, which a screen reader reads
// at once; or as the "whole-page" state, whose focused heading already
// announces it.
export function ReadError({
  message,
  error,
  retrying,
  onRetry,
  placement = "in-page",
}: {
  message: string;
  error: ApiError | undefined;
  retrying: boolean;
  onRetry: () => void;
  placement?: "in-page" | "whole-page";
}): ReactElement {
  return (
    <div className="flex flex-col gap-3" role={placement === "in-page" ? "alert" : undefined}>
      <p>{message}</p>
      {error?.requestId && (
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
