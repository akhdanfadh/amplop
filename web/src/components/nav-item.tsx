import type { ReactElement } from "react";
import type { LucideIcon } from "lucide-react";
import type { LinkProps } from "react-aria-components";
import { SidebarMenuBadge, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";

// A count beside an item's name: value, and label, which says what it
// counts, e.g. "not sent", and is read after it.
type NavCount = { value: number; label: string };

// NavItem is one sidebar link: href, its name, its icon, which alone shows
// on the rail with the name in a tooltip, and count when above zero.
export function NavItem({
  href,
  name,
  icon: Icon,
  isActive,
  onPress,
  count,
}: {
  href: NonNullable<LinkProps["href"]>;
  name: string;
  icon: LucideIcon;
  isActive: boolean;
  onPress: () => void;
  count?: NavCount;
}): ReactElement {
  const counted = count !== undefined && count.value > 0;
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        href={href}
        isActive={isActive}
        // The rail hides the count, so its tooltip says it.
        tooltip={counted ? `${name}, ${count.value} ${count.label}` : name}
        onPress={onPress}
      >
        <Icon aria-hidden="true" />
        <span>{name}</span>
        {counted && (
          <SidebarMenuBadge>
            {count.value}
            <span className="sr-only"> {count.label}</span>
          </SidebarMenuBadge>
        )}
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

// within reports whether pathname is base or a page under it.
export function within(pathname: string, base: string): boolean {
  return pathname === base || pathname.startsWith(`${base}/`);
}
