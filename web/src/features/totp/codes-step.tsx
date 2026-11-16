import { useEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import type { ProblemType } from "@/api/generated/model";
import { uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { LeaveWarning } from "@/components/leave-warning";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { codesText, downloadingMessage, replaceLeaveText, setupLeaveText } from "./text";

const fileName = "inbok-backup-codes.txt";

// notWaitingType answers "I've saved these codes" when the codes no longer
// wait under that token, e.g. another tab showed them again, or made new ones.
export const notWaitingType: ProblemType = "/api/v0/problems/backup-codes-not-waiting";

// CodesStep shows ten backup codes that wait until the member says they
// saved them: in a setup, saying so turns two-factor authentication on; in
// a replacement, it swaps the new codes in for the old. headingLevel keeps
// the step's heading one below the heading it sits under. saving says the
// request runs; error is its failure; isDisabled holds the saved button,
// e.g. while the setup is being canceled. Leaving asks first while
// leaveWarning holds. children follow the saved button, e.g. Cancel setup.
export function CodesStep({
  codes,
  kind,
  headingLevel,
  saving,
  error,
  isDisabled = false,
  leaveWarning,
  onSaved,
  children,
}: {
  codes: readonly string[];
  kind: "setup" | "replace";
  headingLevel: 2 | 3;
  saving: boolean;
  error: ApiError | null;
  isDisabled?: boolean;
  leaveWarning: boolean;
  onSaved: () => void;
  children?: ReactNode;
}): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [message, setMessage] = useState("");
  const Heading = headingLevel === 2 ? "h2" : "h3";

  // A new step: a screen reader starts from its heading.
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  async function copyCodes(): Promise<void> {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setMessage(codesText.copied);
    } catch {
      setMessage(codesText.copyFailed);
    }
  }

  function downloadCodes(): void {
    const file = new Blob([codesText.fileHeader, codes.join("\n"), "\n"], {
      type: "text/plain;charset=utf-8",
    });
    const url = URL.createObjectURL(file);
    const link = document.createElement("a");
    link.href = url;
    link.download = fileName;
    link.click();
    // Leave time for the browser to start reading the download.
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    setMessage(downloadingMessage(fileName));
  }

  return (
    <div className="flex w-full flex-col items-start gap-4" aria-busy={saving}>
      <LeaveWarning
        active={leaveWarning}
        text={kind === "setup" ? setupLeaveText : replaceLeaveText}
        // The codes work only once a signed-in Finish makes them active: after
        // sign-in, setup shows the same ones again, and a replacement's old
        // codes still work (the API's Finish operations).
        onSessionEnd="leave"
      />
      <Heading ref={headingRef} tabIndex={-1} className="font-medium outline-none">
        {kind === "setup" ? codesText.setupHeading : codesText.replaceHeading}
      </Heading>
      {/* A replacement sits under the section that already says what a code
          is for. */}
      <p className="text-sm text-muted-foreground">
        {kind === "setup" ? codesText.setupLead : codesText.replaceLead}
      </p>
      <ul
        aria-label={codesText.list}
        className="grid w-full max-w-md grid-cols-2 gap-x-6 gap-y-2 rounded-md border bg-muted/50 p-4 font-mono text-sm tabular-nums"
      >
        {codes.map((code) => (
          <li key={code} className="select-all">
            {code}
          </li>
        ))}
      </ul>
      <div>
        <div className="flex flex-wrap gap-3">
          <Button type="button" variant="outline" onPress={() => void copyCodes()}>
            {codesText.copy}
          </Button>
          <Button type="button" variant="outline" onPress={downloadCodes}>
            {codesText.download}
          </Button>
        </div>
        {/* Always present, so a screen reader announces the message that
            fills it; empty, it takes no space, not even the gap a flex item
            would. */}
        <output className="block text-sm not-empty:mt-2">{message}</output>
      </div>
      <Button
        type="button"
        className="whitespace-normal"
        isPending={saving}
        isDisabled={isDisabled}
        onPress={onSaved}
      >
        {saving ? (
          <>
            <Spinner />
            {kind === "setup" ? codesText.turningOn : codesText.replacing}
          </>
        ) : (
          codesText.saved
        )}
      </Button>
      {error && (
        <RequestError
          errors={[{ message: userMessage(error) }]}
          // A reference is for reporting a fault, which a refusal is not.
          requestId={uncertainOutcome(error) ? error.requestId : undefined}
          className="w-full"
        />
      )}
      {children}
    </div>
  );
}
