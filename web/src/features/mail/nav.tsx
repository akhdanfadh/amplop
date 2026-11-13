import type { ReactElement } from "react";
import { linkOptions, useLocation } from "@tanstack/react-router";
import { cn } from "cn";
import { Link } from "react-aria-components";
import {
  ArchiveIcon,
  InboxIcon,
  PencilIcon,
  SendIcon,
  ShieldAlertIcon,
  StarIcon,
  Trash2Icon,
  UploadIcon,
  type LucideIcon,
} from "lucide-react";
import { NavItem, within } from "@/components/nav-item";
import { buttonVariants } from "@/components/ui/button";
import { SidebarGroup, SidebarMenu, useSidebar } from "@/components/ui/sidebar";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { viewPlaces, type ViewPlace } from "./places";
import { useOutbox } from "@/features/outgoing-messages/outbox";
import { unfinished } from "@/features/outgoing-messages/text";
import { composeName, notSentLabel, outboxName, placeName } from "./text";

const placeIcon: Record<ViewPlace, LucideIcon> = {
  inbox: InboxIcon,
  starred: StarIcon,
  sent: SendIcon,
  all: ArchiveIcon,
  spam: ShieldAlertIcon,
  trash: Trash2Icon,
};

// MailNav is the mail sidebar's content (DESIGN.md "Sidebar"): Compose,
// the views, and the Outbox with how many of its messages are not sent
// yet. The item whose place is open is marked.
export function MailNav(): ReactElement {
  const pathname = useLocation({ select: (location) => location.pathname });
  const { dismiss: closeDrawer } = useSidebar();
  const outbox = useOutbox();
  // What still asks for the member, so a message sent again stops counting.
  const notSent = outbox.data?.data.messages.filter(unfinished).length ?? 0;

  return (
    <>
      <SidebarGroup>
        <ComposeLink onPress={closeDrawer} />
      </SidebarGroup>
      <SidebarGroup>
        <SidebarMenu>
          {viewPlaces.map((place) => (
            <NavItem
              key={place}
              href={linkOptions({ to: "/mail/$place", params: { place } })}
              name={placeName[place]}
              icon={placeIcon[place]}
              isActive={within(pathname, `/mail/${place}`)}
              onPress={closeDrawer}
            />
          ))}
        </SidebarMenu>
      </SidebarGroup>
      <SidebarGroup>
        <SidebarMenu>
          <NavItem
            href={linkOptions({ to: "/mail/outbox" })}
            name={outboxName}
            icon={UploadIcon}
            count={{ value: notSent, label: notSentLabel }}
            isActive={within(pathname, "/mail/outbox")}
            onPress={closeDrawer}
          />
        </SidebarMenu>
      </SidebarGroup>
    </>
  );
}

// ComposeLink is the one blue button: it opens a new message.
function ComposeLink({ onPress }: { onPress: () => void }): ReactElement {
  const { rail } = useSidebar();

  return (
    <TooltipTrigger isDisabled={!rail}>
      <Link
        href={linkOptions({ to: "/compose" })}
        onPress={onPress}
        aria-label={rail ? composeName : undefined}
        // The pencil keeps its place open or as a rail, in line with the
        // icons below; the button keeps its height, so they don't move.
        className={cn(
          buttonVariants(),
          "w-full justify-start border-0 pl-2 pointer-coarse:pl-3.5",
          rail && "h-9 w-8 justify-center p-0 pointer-coarse:size-11 pointer-coarse:p-0",
        )}
      >
        <PencilIcon aria-hidden="true" />
        {!rail && composeName}
      </Link>
      <Tooltip placement="right">{composeName}</Tooltip>
    </TooltipTrigger>
  );
}
