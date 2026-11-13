import { useEffect, useRef, useState, type ReactElement } from "react";
import { Button } from "@/components/ui/button";
import { sandbox, watch, type FrameState } from "./frame";
import { MessageLoading } from "./loading";
import { bodyFailed } from "./text";

// HtmlBody shows the HTML body at url, which the server sends sanitized
// with its own Content-Security-Policy, in a sandboxed frame as tall as
// the body. Give it a key per URL, so a new body starts loading afresh.
export function HtmlBody({ url, title }: { url: string; title: string }): ReactElement {
  const frame = useRef<HTMLIFrameElement>(null);
  const retryButton = useRef<HTMLButtonElement>(null);
  const [state, setState] = useState<FrameState>("loading");
  // A retry mounts a new frame, which loads the URL again.
  const [attempt, setAttempt] = useState(0);
  // Set by a retry, whose button goes away while the frame loads.
  const restoreFocus = useRef(false);

  useEffect(() => {
    const current = frame.current;
    // Effects run after the frame is rendered, so this never happens.
    if (!current) return undefined;
    return watch(current, setState);
  }, [attempt]);

  useEffect(() => {
    if (!restoreFocus.current || state === "loading") return;
    restoreFocus.current = false;
    // Only when nothing else took focus meanwhile.
    if (document.activeElement !== document.body) return;
    if (state === "failed") retryButton.current?.focus();
    else frame.current?.focus();
  }, [state]);

  function retry(): void {
    restoreFocus.current = true;
    setState("loading");
    setAttempt((n) => n + 1);
  }

  return (
    <div className="flex flex-col gap-3">
      {state === "loading" && <MessageLoading />}
      {state === "failed" && (
        <div className="flex flex-col items-start gap-3" role="alert">
          <p>{bodyFailed}</p>
          <Button ref={retryButton} type="button" variant="outline" onPress={retry}>
            Try again
          </Button>
        </div>
      )}
      <iframe
        key={attempt}
        ref={frame}
        src={url}
        title={title}
        sandbox={sandbox}
        referrerPolicy="no-referrer"
        // Mail is written for a white page, whatever the app's theme.
        className={
          state === "failed" ? "hidden" : "block h-0 w-full rounded-xl bg-sheet scheme-light"
        }
      />
    </div>
  );
}
