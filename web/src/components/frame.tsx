import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useLandmark } from "react-aria/useLandmark";
import { cn } from "@/lib/cn";
import { MemberMenu } from "@/components/member-menu";
import { OfflineBanner } from "@/components/offline-banner";
import { SessionEndedBanner } from "@/features/auth/session-ended-banner";
import { SyncStopped } from "@/features/connected-accounts/sync-stopped";
import { SetupReminder } from "@/features/setup-reminder/reminder";
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
import { endTabAccount, onAccountEndedElsewhere } from "@/features/auth/session-cache";
import { localStore } from "@/lib/local-store";

// The localStorage key that holds whether the sidebar is open, "true" or
// "false"; absent means open.
const openKey = "amplop-sidebar-open";

// The wordmark is a link to the start page, as in most apps.
const wordmarkClass =
  "truncate rounded-md text-lg font-semibold outline-none focus-visible:focus-ring";

// TopBarSlot is where a page's own bar goes: shown says whether the frame
// has a top bar, and box is the top bar's box for it, null until the top
// bar has drawn.
export type TopBarSlot = { shown: boolean; box: HTMLElement | null };

const TopBarSlotContext = createContext<TopBarSlot | null>(null);

// useTopBarSlot returns where a page's own bar goes, e.g. a place's heading
// and Check for new mail, which the page puts in box with a portal while
// shown; null outside a frame, where the page shows them itself (DESIGN.md
// "Phones").
export function useTopBarSlot(): TopBarSlot | null {
  return useContext(TopBarSlotContext);
}

// ProvideTopBarSlot gives children the top bar's box, box, and whether the
// sidebar's layout has a top bar.
function ProvideTopBarSlot({
  box,
  children,
}: {
  box: HTMLElement | null;
  children: ReactNode;
}): ReactElement {
  const { isDrawer } = useSidebar();
  const slot = useMemo(() => ({ shown: isDrawer, box: isDrawer ? box : null }), [isDrawer, box]);
  return <TopBarSlotContext value={slot}>{children}</TopBarSlotContext>;
}

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
  const client = useQueryClient();
  useEffect(
    () => onAccountEndedElsewhere(() => endTabAccount(client, () => window.location.reload())),
    [client],
  );
  const store = useCallback((value: boolean) => {
    setOpen(value);
    localStore.setItem(openKey, String(value));
  }, []);
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  // The page is a landmark React Aria knows, so F6 goes from it to the
  // compose window, the Undo bar, or a toast, and back (DESIGN.md
  // "Compose and reply"): React Aria moves only between landmarks it knows.
  const pageRef = useRef<HTMLDivElement>(null);
  const { landmarkProps } = useLandmark({ role: "region", "aria-label": `${label} page` }, pageRef);

  return (
    <SidebarProvider open={open} onOpenChange={store}>
      <Sidebar label={label}>
        <SidebarHeader className="flex-row items-center gap-2">
          <SidebarTrigger />
          <Link to="/" className={cn(wordmarkClass, "group-data-[collapsible=icon]:hidden")}>
            amplop
          </Link>
        </SidebarHeader>
        <SidebarContent>{nav}</SidebarContent>
        <SidebarFooter>
          <MemberMenu />
        </SidebarFooter>
      </Sidebar>
      <SidebarInset className="h-svh">
        <TopBar slotRef={setSlot} />
        {/* Stacked, the conditions of the moment above the setup still
            owed, an ended session first: nothing here can be saved. */}
        <SessionEndedBanner place="frame" />
        <OfflineBanner />
        <SyncStopped />
        <SetupReminder />
        {/* Relative, so what is placed absolutely in a page, e.g. a
            screen-reader-only label, is placed in this box and scrolls
            with it, instead of stretching the whole page. */}
        <div
          ref={pageRef}
          {...landmarkProps}
          className="relative flex min-h-0 flex-1 flex-col overflow-auto outline-none focus-visible:focus-ring-inset"
        >
          <ProvideTopBarSlot box={slot}>{children}</ProvideTopBarSlot>
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}

// TopBar holds the drawer's button where the sidebar is a drawer, and is
// absent elsewhere. After the button, a page's own bar, which slotRef
// receives the box of, takes the wordmark's place; a page without one
// shows the wordmark. A picking bar takes the button's place too.
function TopBar({ slotRef }: { slotRef: (box: HTMLElement | null) => void }): ReactElement | null {
  const { isDrawer } = useSidebar();
  if (!isDrawer) return null;

  return (
    // The sidebar header's padding, so the drawer's button opens over the
    // top bar's in the same place.
    <header className="group/top-bar flex shrink-0 items-center gap-2 border-b p-3">
      {/* A page's picking bar leads with its own arrow, which stops
          picking (features/mail/picking-bar.tsx). */}
      <SidebarTrigger className="group-has-[[data-slot=picking-bar]]/top-bar:hidden" />
      <Link
        to="/"
        className={cn(
          wordmarkClass,
          "group-has-[[data-slot=top-bar-slot]:not(:empty)]/top-bar:hidden",
        )}
      >
        amplop
      </Link>
      <div
        ref={slotRef}
        data-slot="top-bar-slot"
        className="flex min-w-0 flex-1 items-center gap-2 empty:hidden"
      />
    </header>
  );
}
