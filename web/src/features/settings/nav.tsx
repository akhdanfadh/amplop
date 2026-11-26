import type { ReactElement } from "react";
import { linkOptions, useLocation, type ToOptions } from "@tanstack/react-router";
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
import { backToMail, pageName, thisAmplop } from "./text";

// A settings page, as the router names it.
type SettingsPath = Extract<NonNullable<ToOptions["to"]>, `/settings/${string}`>;

// SettingsNav is the settings sidebar's content (DESIGN.md "Settings and
// signed-out pages"): Back to mail, the member's own pages, then, for
// administrators, "This amplop".
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
          <SidebarGroupLabel elementType="h2">{thisAmplop}</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {item("/settings/members", pageName.members, UsersIcon)}
              {item("/settings/registration", pageName.registration, UserPlusIcon)}
              {item("/settings/system-email", pageName.systemEmail, MailIcon)}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      )}
    </>
  );
}
