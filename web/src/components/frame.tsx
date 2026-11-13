import { useCallback, useEffect, useState, type ReactElement, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { cn } from "@/lib/cn";
import { MemberMenu } from "@/components/member-menu";
import { OfflineBanner } from "@/components/offline-banner";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { onAccountEndedElsewhere } from "@/features/auth/session-cache";
import { localStore } from "@/lib/local-store";

// The localStorage key that holds whether the sidebar is open, "true" or
// "false"; absent means open.
const openKey = "inbok-sidebar-open";

// The wordmark is a link to the start page, as in most apps.
const wordmarkClass =
  "truncate rounded-md text-lg font-semibold outline-none focus-visible:focus-ring";

// Frame is the signed-in app around a page (DESIGN.md "Panes and widths"):
// a sidebar named label with nav in it and the member menu at the bottom,
// and children beside it. Below 1024 px the sidebar is a drawer that a top
// bar opens. The frame fills the window; children scroll in their own box,
// so the bars above them stay in view.
export function Frame({
  label,
  nav,
  children,
}: {
  label: string;
  nav: ReactNode;
  children: ReactNode;
}): ReactElement {
  // The member's choice for the full layout, kept for the next visit; the
  // sidebar keeps the rail's and the drawer's itself.
  const [open, setOpen] = useState(() => localStore.getItem(openKey) !== "false");
  // Another tab signed out, or another member signed in there: this tab's
  // member data may belong to no one now, so the page loads again, and its
  // guard reads whose session the browser holds.
  useEffect(() => onAccountEndedElsewhere(() => window.location.reload()), []);
  const store = useCallback((value: boolean) => {
    setOpen(value);
    localStore.setItem(openKey, String(value));
  }, []);

  return (
    <SidebarProvider open={open} onOpenChange={store}>
      <Sidebar label={label}>
        <SidebarHeader className="flex-row items-center gap-2">
          <SidebarTrigger />
          <Link to="/" className={cn(wordmarkClass, "group-data-[collapsible=icon]:hidden")}>
            inbok
          </Link>
        </SidebarHeader>
        <SidebarContent>{nav}</SidebarContent>
        <SidebarFooter>
          <MemberMenu />
        </SidebarFooter>
      </Sidebar>
      <SidebarInset className="h-svh">
        <TopBar />
        <OfflineBanner />
        {/* Relative, so what is placed absolutely in a page, e.g. a
            screen-reader-only label, is placed in this box and scrolls
            with it, instead of stretching the whole page. */}
        <div className="relative flex min-h-0 flex-1 flex-col overflow-auto">{children}</div>
      </SidebarInset>
    </SidebarProvider>
  );
}

// TopBar holds the drawer's button where the sidebar is a drawer, and is
// absent elsewhere.
function TopBar(): ReactElement | null {
  const { isDrawer } = useSidebar();
  if (!isDrawer) return null;

  return (
    <header className="flex shrink-0 items-center gap-2 border-b px-2 py-2">
      <SidebarTrigger />
      <Link to="/" className={wordmarkClass}>
        inbok
      </Link>
    </header>
  );
}
