import type { ReactElement } from "react";
import { Link, linkOptions, useLocation, type ToOptions } from "@tanstack/react-router";
import {
  ArrowLeftIcon,
  LinkIcon,
  MailIcon,
  ShieldIcon,
  UserPlusIcon,
  UsersIcon,
} from "lucide-react";
import { useSignedInSession } from "@/features/auth/session";
import { NavItem, within } from "@/components/nav-item";
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  useSidebar,
} from "@/components/ui/sidebar";
import { mailHref } from "@/features/mail/places";
import { backToMail, lockedAfter, lockedBefore, pageName, thisInbok } from "./text";
import { textLink } from "@/components/link-style";

// A settings page, as the router names it.
type SettingsPath = Extract<NonNullable<ToOptions["to"]>, `/settings/${string}`>;

// SettingsNav is the settings sidebar's content (DESIGN.md "Settings and
// signed-out pages"): Back to mail, the member's own pages, then, for
// administrators, "This inbok", locked until they set up two-factor
// authentication.
export function SettingsNav(): ReactElement {
  const pathname = useLocation({ select: (location) => location.pathname });
  const { dismiss: closeDrawer } = useSidebar();
  const session = useSignedInSession();
  const item = (to: SettingsPath, name: string, icon: typeof ShieldIcon) => (
    <NavItem
      href={linkOptions({ to })}
      name={name}
      icon={icon}
      isActive={within(pathname, to)}
      onPress={closeDrawer}
    />
  );

  return (
    <>
      <SidebarGroup>
        <SidebarMenu>
          <NavItem
            href={mailHref()}
            name={backToMail}
            icon={ArrowLeftIcon}
            isActive={false}
            onPress={closeDrawer}
          />
        </SidebarMenu>
      </SidebarGroup>
      <SidebarGroup>
        <SidebarMenu>
          {item("/settings/security", pageName.security, ShieldIcon)}
          {item("/settings/connected-accounts", pageName.connectedAccounts, LinkIcon)}
        </SidebarMenu>
      </SidebarGroup>
      {session?.role === "admin" && (
        <SidebarGroup>
          <SidebarGroupLabel elementType="h2">{thisInbok}</SidebarGroupLabel>
          <SidebarGroupContent>
            {session.totpRequired ? (
              <p className="px-2 text-sm text-muted-foreground group-data-[collapsible=icon]:hidden">
                {lockedBefore}{" "}
                <Link to="/settings/security" className={textLink} onClick={closeDrawer}>
                  {pageName.security}
                </Link>{" "}
                {lockedAfter}
              </p>
            ) : (
              <SidebarMenu>
                {item("/settings/members", pageName.members, UsersIcon)}
                {item("/settings/registration", pageName.registration, UserPlusIcon)}
                {item("/settings/system-email", pageName.systemEmail, MailIcon)}
              </SidebarMenu>
            )}
          </SidebarGroupContent>
        </SidebarGroup>
      )}
    </>
  );
}
