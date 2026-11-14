import { useRef, useState, type ReactElement } from "react";
import { Link } from "react-aria-components";
import {
  File,
  FileArchive,
  FileImage,
  FileMusic,
  FileSpreadsheet,
  FileText,
  FileVideoCamera,
} from "lucide-react";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { attachmentsLabel, formatSize, splitName, unnamedFile } from "./text";

// A part of a message offered as a download.
export type Attachment = {
  partId: string;
  // The name the sender gave it; empty when none.
  filename: string;
  mediaType: string;
  // In bytes, as the provider reports it.
  size: number;
  // The API's part route, which sends the file.
  url: `/api/${string}`;
};

// Attachments shows a message's downloads as chips: an icon for the kind
// of file, its name, and its size (DESIGN.md "Reading pane"). Each
// downloads its file, under the sender's name for it, or the server's when
// it has none.
export function Attachments({
  attachments,
}: {
  attachments: readonly Attachment[];
}): ReactElement | null {
  if (attachments.length === 0) return null;
  return (
    <ul aria-label={attachmentsLabel} className="flex flex-wrap gap-2">
      {attachments.map((attachment) => (
        <li key={attachment.partId} className="max-w-full">
          <Chip attachment={attachment} />
        </li>
      ))}
    </ul>
  );
}

// Chip is one attachment's link. A name too long for the chip is cut in
// the middle, so its end, which tells similar files apart, always shows;
// screen readers read the whole name, and a tooltip shows it while the
// name is cut.
function Chip({ attachment }: { attachment: Attachment }): ReactElement {
  const nameRef = useRef<HTMLElement>(null);
  const [tooltipOpen, setTooltipOpen] = useState(false);
  const name = attachment.filename || unnamedFile;
  const { start, beforeExtension, extension } = splitName(name);

  return (
    <TooltipTrigger
      isOpen={tooltipOpen}
      // Only a cut name needs it; one shown whole would be read twice.
      onOpenChange={(open) => setTooltipOpen(open && isCut(nameRef.current))}
    >
      <Link
        href={attachment.url}
        // React Aria's router leaves a link with download to the browser.
        download={attachment.filename || true}
        className="flex min-h-9 max-w-72 items-center gap-2 rounded-lg border border-input bg-card px-3 py-1.5 text-sm transition-colors outline-none hover:bg-accent data-focus-visible:focus-ring pointer-coarse:min-h-11"
      >
        {fileIcon(attachment.mediaType)}
        {/* The name whole for screen readers: in parts, the shown one would
            be read with a space at each split. */}
        <span className="sr-only">{name}</span>
        {/* The sender's text, so bdi keeps a direction override in it from
            reordering the size after it, and an Arabic or Hebrew name runs
            right to left, its extension on its left. */}
        <bdi ref={nameRef} aria-hidden className="flex min-w-0">
          {/* The start gives way first, down to a character and its
              ellipsis, so what follows never reads as the whole name; then
              the characters before the extension; the extension never. */}
          {start && <span className="min-w-[2ch] shrink-[1000] truncate">{start}</span>}
          <span className="truncate whitespace-pre">{beforeExtension}</span>
          {extension && <span className="shrink-0 whitespace-pre">{extension}</span>}
        </bdi>
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {formatSize(attachment.size)}
        </span>
      </Link>
      <Tooltip>
        {/* A name without spaces, e.g. a camera's, still wraps. */}
        <span className="min-w-0 wrap-anywhere">{name}</span>
      </Tooltip>
    </TooltipTrigger>
  );
}

// isCut reports whether either part of name, a chip's shown name, is cut
// to fit the chip.
function isCut(name: HTMLElement | null): boolean {
  return Array.from(name?.children ?? []).some((part) => part.scrollWidth > part.clientWidth);
}

const iconClass = "size-4 shrink-0 text-muted-foreground";

// fileIcon returns the icon for a file of mediaType, as the sender
// declared it.
function fileIcon(mediaType: string): ReactElement {
  const [type = "", subtype = ""] = mediaType.toLowerCase().split("/");
  if (type === "image") return <FileImage aria-hidden className={iconClass} />;
  if (type === "audio") return <FileMusic aria-hidden className={iconClass} />;
  if (type === "video") return <FileVideoCamera aria-hidden className={iconClass} />;
  // Before documents: an Office spreadsheet's type names both.
  if (subtype.includes("sheet") || subtype.includes("excel") || subtype === "csv")
    return <FileSpreadsheet aria-hidden className={iconClass} />;
  if (type === "text" || subtype === "pdf" || subtype.includes("document"))
    return <FileText aria-hidden className={iconClass} />;
  if (/zip|compressed|tar|rar|7z/.test(subtype))
    return <FileArchive aria-hidden className={iconClass} />;
  return <File aria-hidden className={iconClass} />;
}
