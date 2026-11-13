import type { ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { linkOptions, useNavigate } from "@tanstack/react-router";
import { cn } from "@/lib/cn";
import { Link } from "react-aria-components";
import {
  ChevronsUpDownIcon,
  LogOutIcon,
  MonitorIcon,
  MoonIcon,
  SettingsIcon,
  SunIcon,
  SunMoonIcon,
  type LucideIcon,
} from "lucide-react";
import { useDeleteCurrentSession } from "@/api/generated/inbok";
import { Button, buttonVariants } from "@/components/ui/button";
import { Popover, PopoverTrigger } from "@/components/ui/popover";
import { Separator } from "@/components/ui/separator";
import { useSidebar } from "@/components/ui/sidebar";
import { showError } from "@/components/ui/toast";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { useSignedInSession } from "@/features/auth/session";
import { clearAccountCache } from "@/features/auth/session-cache";
import { sessionMessage } from "@/features/auth/session-error";
import { useTheme } from "@/hooks/use-theme";
import type { Theme } from "@/lib/theme";

const unauthorized = 401;

const themeChoices: { theme: Theme; name: string; icon: LucideIcon }[] = [
  { theme: "light", name: "Light", icon: SunIcon },
  { theme: "dark", name: "Dark", icon: MoonIcon },
  { theme: "system", name: "System", icon: MonitorIcon },
];

// Settings and Sign out look like a menu's rows.
const rowClass = cn(
  buttonVariants({ variant: "ghost", size: "sm" }),
  "w-full justify-start font-normal",
);

// MemberMenu is the signed-in member at the bottom of the sidebar, opening
// Settings, the theme choice, and Sign out.
//
// DESIGN: a popover, not a menu: the theme is one choice of three shown
// side by side, which a menu's rows can't hold. It reads as a menu: who is
// signed in, the member's places, then Sign out apart. Tab moves between
// the parts and arrow keys between the themes, each an icon named by its
// tooltip; the visible "Theme" label names the three icons together.
export function MemberMenu(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const { rail, dismiss } = useSidebar();
  const [theme, setTheme] = useTheme();
  const email = useSignedInSession()?.email ?? "";
  const signOut = useDeleteCurrentSession({
    mutation: {
      onSuccess: finishSignOut,
      onError: (err) => {
        // An absent or expired session is already signed out; Go clears its cookie.
        if (err.status === unauthorized) finishSignOut();
        else showError("Couldn't sign out", sessionMessage(err, "sign-out"));
      },
    },
  });

  function finishSignOut(): void {
    clearAccountCache(client);
    // Sign out leaves without the page's leave warning: the session is
    // gone, and a page that stayed would show a signed-out member's work.
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }

  return (
    <PopoverTrigger>
      <TooltipTrigger isDisabled={!rail}>
        <Button
          variant="ghost"
          // Names the member on the rail, where only the initial shows.
          aria-label={rail ? `Member menu, ${email}` : undefined}
          isPending={signOut.isPending}
          // The initial keeps its place open or as a rail: centred on the
          // rail, like the icons above it.
          className="h-10 w-full justify-start gap-2 border-0 py-0 pr-2 pl-0.5 text-left group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0 pointer-coarse:h-11 pointer-coarse:pl-2 pointer-coarse:group-data-[collapsible=icon]:w-11"
        >
          <span
            aria-hidden="true"
            className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent text-xs font-semibold"
          >
            {email.charAt(0).toUpperCase()}
          </span>
          <span className="min-w-0 flex-1 truncate font-normal group-data-[collapsible=icon]:hidden">
            {email}
          </span>
          <ChevronsUpDownIcon
            aria-hidden="true"
            className="text-muted-foreground group-data-[collapsible=icon]:hidden"
          />
        </Button>
        <Tooltip placement="right">{email}</Tooltip>
      </TooltipTrigger>
      <Popover aria-label="Member menu" placement={rail ? "end bottom" : "top start"}>
        {({ close }) => (
          <>
            <p className="truncate px-2.5 pt-1.5 pb-1 text-xs text-muted-foreground">
              Signed in as {email}
            </p>
            <Separator className="-mx-1 my-1 w-auto" />
            <Link
              href={linkOptions({ to: "/settings" })}
              className={rowClass}
              onPress={() => {
                close();
                dismiss();
              }}
            >
              <SettingsIcon aria-hidden="true" />
              Settings
            </Link>
            <div className="flex items-center justify-between gap-2 py-1 pr-1 pl-2.5">
              {/* Spaced as the rows' icon and word are, so the labels line up. */}
              <span id="theme-label" className="flex items-center gap-1">
                <SunMoonIcon aria-hidden="true" className="size-4" />
                Theme
              </span>
              <ToggleGroup
                aria-labelledby="theme-label"
                selectionMode="single"
                disallowEmptySelection
                selectedKeys={[theme]}
                onSelectionChange={(keys) => {
                  const picked = themeChoices.find(({ theme: choice }) => keys.has(choice));
                  if (picked) setTheme(picked.theme);
                }}
              >
                {themeChoices.map(({ theme: choice, name, icon: Icon }) => (
                  <TooltipTrigger key={choice}>
                    <ToggleGroupItem id={choice} aria-label={name}>
                      <Icon aria-hidden="true" />
                    </ToggleGroupItem>
                    <Tooltip>{name}</Tooltip>
                  </TooltipTrigger>
                ))}
              </ToggleGroup>
            </div>
            <Separator className="-mx-1 my-1 w-auto" />
            <Button
              variant="ghost"
              size="sm"
              className={rowClass}
              onPress={() => {
                close();
                signOut.mutate();
              }}
            >
              <LogOutIcon aria-hidden="true" />
              Sign out
            </Button>
          </>
        )}
      </Popover>
    </PopoverTrigger>
  );
}
