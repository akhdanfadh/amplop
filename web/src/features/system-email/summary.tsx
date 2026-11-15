import type { ReactElement, ReactNode } from "react";
import type { SystemEmailSettings } from "@/api/generated/model";
import { securityLabel, summaryText } from "./text";

// Summary lists the saved settings, the password only as saved or not.
export function Summary({ settings }: { settings: SystemEmailSettings }): ReactElement {
  const rows: [string, ReactNode][] = [
    [summaryText.server, settings.host],
    [summaryText.port, String(settings.port)],
    [
      summaryText.security,
      settings.security === "none" ? summaryText.noSecurity : securityLabel(settings.security),
    ],
    [summaryText.username, settings.username || summaryText.noUsername],
    [
      summaryText.password,
      settings.hasPassword ? summaryText.passwordSaved : summaryText.noPassword,
    ],
    // How a mail app shows a sender (RFC 5322 section 3.4's name-addr); the
    // name is isolated, so a right-to-left one keeps the brackets in place.
    [
      summaryText.from,
      <>
        <bdi>{settings.fromName}</bdi> &lt;{settings.fromAddress}&gt;
      </>,
    ],
  ];
  return (
    // A narrow page puts each name over its value, so a long host keeps a
    // readable width.
    <dl className="grid w-full grid-cols-1 gap-x-6 text-sm @xs:grid-cols-[minmax(0,auto)_minmax(0,1fr)] @xs:gap-y-2">
      {rows.map(([term, value]) => (
        <div key={term} className="contents">
          <dt className="text-muted-foreground">{term}</dt>
          {/* A host, an address, or a long name breaks where it must. */}
          <dd className="mb-2 wrap-anywhere @xs:mb-0">{value}</dd>
        </div>
      ))}
    </dl>
  );
}
