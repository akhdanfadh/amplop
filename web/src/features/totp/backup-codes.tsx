import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";

// Codes stay in the parent component's memory until explicitly dismissed.
export function BackupCodes({
  codes,
  active,
  source,
  onDone,
}: {
  codes: string[];
  active: boolean;
  source: "enrollment" | "regeneration";
  onDone: () => void;
}) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (active) headingRef.current?.focus();
  }, [active]);

  async function copyCodes(): Promise<void> {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setMessage("Backup codes copied.");
    } catch {
      setMessage("Could not copy. Download the codes or select and copy them manually.");
    }
  }

  function downloadCodes(): void {
    const file = new Blob(
      ["inbok backup codes\nUse each code once. Keep these private.\n\n", codes.join("\n"), "\n"],
      { type: "text/plain;charset=utf-8" },
    );
    const url = URL.createObjectURL(file);
    const link = document.createElement("a");
    link.href = url;
    link.download = "inbok-backup-codes.txt";
    link.click();
    // Leave time for the browser to start reading the download.
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return (
    <section className="flex flex-col gap-6" aria-labelledby="backup-codes-heading">
      <h2
        ref={headingRef}
        tabIndex={-1}
        id="backup-codes-heading"
        className="text-xl font-semibold"
      >
        Save your backup codes
      </h2>
      <p>
        {source === "enrollment"
          ? "Two-factor authentication is enabled. Your other sessions have ended."
          : "Your old backup codes no longer work. Your sessions stay signed in."}
      </p>
      <p>
        Each code can sign you in once if you lose access to your authenticator. These codes will
        not be shown again. Save them somewhere private before leaving.
      </p>
      <ul aria-label="Backup codes" className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {codes.map((code) => (
          <li key={code}>
            <code className="select-all">{code}</code>
          </li>
        ))}
      </ul>
      <div className="flex flex-wrap gap-3">
        <Button
          type="button"
          variant="outline"
          className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
          onClick={() => void copyCodes()}
        >
          Copy codes
        </Button>
        <Button
          type="button"
          variant="outline"
          className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
          onClick={downloadCodes}
        >
          Download codes
        </Button>
      </div>
      <output>{message}</output>
      <Button
        type="button"
        className="h-auto min-h-11 whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        onClick={onDone}
      >
        I've saved these codes
      </Button>
    </section>
  );
}
