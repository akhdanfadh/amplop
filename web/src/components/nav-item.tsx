import type { ReactElement } from "react";
import type { LucideIcon } from "lucide-react";
import type { LinkProps } from "react-aria-components";
import { SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";

// NavItem is one sidebar link: href, its name, and its icon, which alone
// shows on the rail with the name in a tooltip.
export function NavItem({
  href,
  name,
  icon: Icon,
  isActive,
  onPress,
}: {
  href: NonNullable<LinkProps["href"]>;
  name: string;
  icon: LucideIcon;
  isActive: boolean;
  onPress: () => void;
}): ReactElement {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton href={href} isActive={isActive} tooltip={name} onPress={onPress}>
        <Icon aria-hidden="true" />
        <span>{name}</span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

// within reports whether pathname is base or a page under it.
export function within(pathname: string, base: string): boolean {
  return pathname === base || pathname.startsWith(`${base}/`);
}
