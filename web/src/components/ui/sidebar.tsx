import * as React from "react";
import { cn } from "@/lib/cn";
import {
  Button as ButtonPrimitive,
  Link as LinkPrimitive,
  type ButtonProps,
  type LinkProps,
} from "react-aria-components";
import { PanelLeftIcon } from "lucide-react";

import { useLayout } from "@/hooks/use-layout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";

// The drawer's width. The open sidebar (240 px) and the rail (56 px; 68 px
// for a coarse pointer, so its 44 px rows fit) are set on the wrapper.
const drawerStyle: React.CSSProperties & Record<"--sheet-width", string> = {
  "--sheet-width": "18rem",
};

// The shortcut that opens and closes the sidebar: B with Ctrl, or with Cmd
// on a Mac, as in shadcn's sidebar and Gmail's.
const shortcutLetter = "b";
const shortcutCode = "KeyB";
const shortcutMac = "Cmd+B";
const shortcutOther = "Ctrl+B";

const triggerSelector = "[data-slot=sidebar-trigger]";
const drawerSelector = "[data-sidebar=drawer]";

// The sidebar's state as its parts and the app read it.
type SidebarState = {
  // "collapsed" where this part shows as a rail, "expanded" where it is
  // open: beside the content, or in the drawer.
  state: "expanded" | "collapsed";
  // The window has no room for a sidebar or a rail: only the drawer shows
  // it.
  isDrawer: boolean;
  drawerOpen: boolean;
  // Shown as a rail here, its items as icons alone.
  rail: boolean;
  toggleSidebar: () => void;
  // dismiss closes the drawer, e.g. after the member picks a place in it;
  // the full layout's sidebar stays as the member left it.
  dismiss: () => void;
};

type SidebarContextProps = SidebarState & {
  // The full layout's sidebar is open beside the content.
  open: boolean;
  // The rail layout: a rail that expands into the drawer.
  railLayout: boolean;
  // This part of the sidebar is inside the drawer.
  inDrawer: boolean;
  setDrawerOpen: (open: boolean) => void;
  // The sidebar beside the content, open or as a rail.
  containerRef: React.RefObject<HTMLElement | null>;
};

// An item opens a page, as a link that can be the current page, or acts,
// as a button.
type SidebarButtonProps =
  | (Omit<LinkProps, "className" | "ref"> & {
      href: NonNullable<LinkProps["href"]>;
      isActive?: boolean;
    })
  | (Omit<ButtonProps, "className" | "ref"> & { href?: never; isActive?: never });

const SidebarContext = React.createContext<SidebarContextProps | null>(null);

// useSidebarContext returns the nearest SidebarProvider's state, the
// drawer's own inside the drawer, and throws outside a provider.
function useSidebarContext(): SidebarContextProps {
  const context = React.useContext(SidebarContext);
  if (!context) {
    throw new Error("useSidebar must be used within a SidebarProvider.");
  }

  return context;
}

// useSidebar returns the sidebar's state for the app's own parts of it,
// e.g. a menu that closes the drawer after a choice.
function useSidebar(): SidebarState {
  return useSidebarContext();
}

// isToggleKey reports whether event is Ctrl+B, or Cmd+B, alone: no Shift
// or Alt, not a held key's repeat, not typed into a text field, and not
// in a dialog or menu other than the drawer.
function isToggleKey(event: KeyboardEvent): boolean {
  // B is the letter in any Latin layout, Caps Lock or not, so Dvorak's
  // Ctrl+X stays cut; in any other layout, e.g. Cyrillic, it is the key
  // where QWERTY has it.
  const latin = /^[a-z]$/i.test(event.key);
  if (latin ? event.key.toLowerCase() !== shortcutLetter : event.code !== shortcutCode) {
    return false;
  }
  if (event.repeat || event.defaultPrevented) return false;
  if (event.shiftKey || event.altKey || event.ctrlKey === event.metaKey) return false;
  // In a field Ctrl+B already means something: back one character on a
  // Mac, bold in an editor.
  const target = event.target instanceof Element ? event.target : null;
  if (target?.closest("input, textarea, select, [contenteditable]")) return false;
  // An open popover, menu, or dialog has the member's attention; the
  // sidebar would change out of sight behind it, and the drawer would
  // close around a popover opened from it.
  const overlay = target?.closest("[role=dialog], [role=alertdialog], [role=menu]");
  return !overlay || overlay.matches("[data-slot=sheet]");
}

// SidebarProvider holds the sidebar's state. open is the member's choice
// for the full layout, which the owner stores through onOpenChange. The
// drawer, which the rail layout expands into and the narrower layouts
// show alone, starts closed and closes again when the window leaves those
// layouts. Ctrl/Cmd+B toggles the one in use.
//
// DESIGN: inbok's copy differs from shadcn's in all of these. One left
// sidebar that collapses to a rail: no offcanvas, floating, inset, or
// right-hand variant, and no SidebarRail: a strip on the sidebar's edge
// that only a mouse reaches (ADR031, "Frame").
// The window's layout (useLayout) picks sidebar, rail, or drawer, not a
// 768 px breakpoint, so CSS never hides what React chose. In the rail
// layout the rail expands into the drawer, over the content, as a
// Material 3 navigation rail expands into a modal drawer; React Aria's
// modal then closes it on Escape or a press beside it and keeps focus in
// it. Its owner stores the open state, so nothing goes to a cookie.
// Sidebar is a nav landmark with a name, its menus are lists, and
// SidebarInset is a div, since the page holds its own main. It changes
// size at once, since DESIGN.md animates only colour, opacity, and
// transform. It is 240 px open and 56 px as a rail (68 px coarse), with
// 12 px padding; icons keep one column, open or as a rail, and a group's
// label becomes a hairline there. The open item, only ever a link, has the
// selection token, a 2 px bar, and aria-current. SidebarMenuBadge follows
// the name inside the item rather than lying over its end, so a long name
// gives way to it, and no variant, sm or lg size, or show-on-hover action.
// Focus rings follow React Aria's data-focus-visible.
function SidebarProvider({
  open: fullOpen,
  onOpenChange,
  className,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const layout = useLayout();
  const isDrawer = layout === "drawer" || layout === "phone";
  const railLayout = layout === "rail";
  const hasDrawer = layout !== "full";
  const containerRef = React.useRef<HTMLElement>(null);
  const wrapperRef = React.useRef<HTMLDivElement>(null);
  const [drawerOpen, setDrawerOpen] = React.useState(false);
  // Where the drawer is: beside the rail, alone in the drawer and phone
  // layouts, or nowhere in the full layout.
  const drawerPlace = railLayout ? "rail" : hasDrawer ? "alone" : "none";
  // Set while rendering, as React advises for state that follows a value:
  // the drawer closes when the window moves it elsewhere, or it would
  // reappear when the window comes back to a layout with one. The drawer
  // and phone layouts show the same drawer, so it stays open between them.
  const [shownPlace, setShownPlace] = React.useState(drawerPlace);
  if (drawerPlace !== shownPlace) {
    setShownPlace(drawerPlace);
    setDrawerOpen(false);
  }

  // The part of the sidebar, or of the page beside it, that last had focus,
  // the drawer's included: React passes focus events up through portals.
  const lastFocused = React.useRef<Element | null>(null);

  // A layout change can take away the focused part, e.g. the drawer, a top
  // bar's button past 1024 px, the wordmark the rail hides, or a part of
  // the page that draws differently at the new width. Focus would fall to
  // the page, which a screen reader does not announce, and Tab would go on
  // from where the part was: for the drawer, drawn at the page's end, that
  // is the browser's own controls. It goes to the sidebar's button shown
  // now instead. Focus still on screen, e.g. in the page, stays.
  // NOTE: not a layout effect: a closed drawer's modal keeps the rest of
  // the page inert, where nothing takes focus, until its own effects clean
  // up (React Aria's useModalOverlay).
  React.useEffect(() => {
    const last = lastFocused.current;
    if (!last || (last.isConnected && last.checkVisibility())) return;
    const active = document.activeElement;
    // A hidden part can keep focus for a moment before the browser lets go.
    const kept =
      active &&
      active !== document.body &&
      active.checkVisibility() &&
      !active.closest(drawerSelector);
    if (kept) return;
    const triggers = wrapperRef.current?.querySelectorAll<HTMLElement>(triggerSelector) ?? [];
    Array.from(triggers)
      .find((trigger) => trigger.checkVisibility())
      ?.focus();
  }, [layout]);

  const open = layout === "full" && fullOpen;

  // Set by collapse when focus was in the sidebar, for the layout effect
  // below.
  const keepFocus = React.useRef(false);
  const collapse = React.useCallback(() => {
    keepFocus.current = containerRef.current?.contains(document.activeElement) ?? false;
    onOpenChange(false);
  }, [onOpenChange]);

  // Focus on a part the rail hides, e.g. the wordmark, would fall to the
  // page, and Tab would start again from its top; it goes to the sidebar's
  // own button instead.
  React.useLayoutEffect(() => {
    if (open || !keepFocus.current) return;
    keepFocus.current = false;
    const container = containerRef.current;
    const active = document.activeElement;
    if (active && container?.contains(active) && active.checkVisibility()) return;
    container?.querySelector<HTMLElement>(triggerSelector)?.focus();
  }, [open]);

  const toggleSidebar = React.useCallback(() => {
    if (hasDrawer) setDrawerOpen((value) => !value);
    else onOpenChange(!fullOpen);
  }, [hasDrawer, fullOpen, onOpenChange]);

  const dismiss = React.useCallback(() => setDrawerOpen(false), []);

  React.useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isToggleKey(event)) return;
      event.preventDefault();
      if (hasDrawer) setDrawerOpen((value) => !value);
      else if (fullOpen) collapse();
      else onOpenChange(true);
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [hasDrawer, fullOpen, collapse, onOpenChange]);

  const state = open ? "expanded" : "collapsed";
  const rail = !isDrawer && !open;

  const contextValue = React.useMemo<SidebarContextProps>(
    () => ({
      state,
      open,
      isDrawer,
      drawerOpen,
      setDrawerOpen,
      rail,
      railLayout,
      inDrawer: false,
      toggleSidebar,
      dismiss,
      containerRef,
    }),
    [state, open, isDrawer, drawerOpen, rail, railLayout, toggleSidebar, dismiss],
  );

  return (
    <SidebarContext.Provider value={contextValue}>
      <div
        ref={wrapperRef}
        onFocus={(event) => {
          lastFocused.current = event.target;
        }}
        data-slot="sidebar-wrapper"
        className={cn(
          "group/sidebar-wrapper flex min-h-svh w-full [--sidebar-width-icon:3.5rem] [--sidebar-width:15rem] pointer-coarse:[--sidebar-width-icon:4.25rem]",
          className,
        )}
        {...props}
      >
        {children}
      </div>
    </SidebarContext.Provider>
  );
}

// Sidebar is the sidebar itself, a navigation landmark named label: beside
// the content, open or as a rail, and in the drawer, a sheet with the same
// name, which the rail layout expands into and the narrower layouts show
// alone.
function Sidebar({
  label,
  className,
  children,
  ...props
}: Omit<React.ComponentProps<"nav">, "ref"> & {
  label: string;
}) {
  const context = useSidebarContext();
  const { isDrawer, railLayout, state, drawerOpen, setDrawerOpen, containerRef } = context;
  // In the drawer the sidebar is open, so its parts show their names.
  const drawerContext = React.useMemo<SidebarContextProps>(
    () => ({ ...context, state: "expanded", rail: false, inDrawer: true }),
    [context],
  );

  const drawer = (
    <Sheet
      isOpen={drawerOpen}
      onOpenChange={setDrawerOpen}
      className="gap-0 bg-sidebar p-0 text-sidebar-foreground"
      style={drawerStyle}
      side="left"
      showCloseButton={false}
      data-sidebar="drawer"
    >
      <SheetTitle className="sr-only">{label}</SheetTitle>
      <SidebarContext.Provider value={drawerContext}>
        <nav
          aria-label={label}
          data-slot="sidebar"
          className={cn("flex h-full w-full flex-col", className)}
          {...props}
        >
          {children}
        </nav>
      </SidebarContext.Provider>
    </Sheet>
  );

  if (isDrawer) return drawer;

  return (
    <div
      className="group block text-sidebar-foreground"
      data-state={state}
      data-collapsible={state === "collapsed" ? "icon" : ""}
      data-slot="sidebar"
    >
      {/* Keeps the sidebar's room in the page. */}
      <div
        data-slot="sidebar-gap"
        className="relative w-(--sidebar-width) group-data-[collapsible=icon]:w-(--sidebar-width-icon)"
      />
      <nav
        ref={containerRef}
        aria-label={label}
        data-slot="sidebar-container"
        className={cn(
          "fixed inset-y-0 left-0 z-20 flex h-svh w-(--sidebar-width) border-r group-data-[collapsible=icon]:w-(--sidebar-width-icon)",
          className,
        )}
        {...props}
      >
        <div data-slot="sidebar-inner" className="flex size-full flex-col bg-sidebar">
          {children}
        </div>
      </nav>
      {railLayout && drawer}
    </div>
  );
}

// SidebarTrigger opens and closes the sidebar, or the drawer; its name says
// which it does now, and its tooltip adds the shortcut.
function SidebarTrigger({ className, onPress, ...props }: React.ComponentProps<typeof Button>) {
  const { toggleSidebar, isDrawer, railLayout, inDrawer, open, drawerOpen } = useSidebarContext();
  let name = open ? "Collapse sidebar" : "Expand sidebar";
  // The rail expands into the drawer, which collapses back into the rail.
  if (railLayout) name = inDrawer ? "Collapse sidebar" : "Expand sidebar";
  if (isDrawer) name = drawerOpen ? "Close sidebar" : "Open sidebar";
  // Apple keyboards say Cmd; the rest say Ctrl. Words, not the Command
  // symbol, which no keyboard types (DESIGN.md "Wording").
  const shortcut = /Mac|iPhone|iPad/.test(navigator.userAgent) ? shortcutMac : shortcutOther;

  // NOTE: React Aria's Button drops aria-keyshortcuts, so the tooltip, its
  // description, is what tells assistive technology the shortcut.
  return (
    <TooltipTrigger>
      <Button
        data-slot="sidebar-trigger"
        variant="ghost"
        size="icon-sm"
        aria-label={name}
        className={className}
        onPress={(event) => {
          onPress?.(event);
          toggleSidebar();
        }}
        {...props}
      >
        <PanelLeftIcon aria-hidden="true" />
      </Button>
      <Tooltip>{`${name} (${shortcut})`}</Tooltip>
    </TooltipTrigger>
  );
}

// SidebarInset is the content beside the sidebar.
function SidebarInset({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-inset"
      className={cn("relative flex w-full min-w-0 flex-1 flex-col bg-background", className)}
      {...props}
    />
  );
}

function SidebarHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-header"
      className={cn("flex flex-col gap-2 p-3", className)}
      {...props}
    />
  );
}

function SidebarFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-footer"
      className={cn("flex flex-col gap-2 p-3", className)}
      {...props}
    />
  );
}

// SidebarInput is a text field in the sidebar, e.g. a filter in its
// header, as tall as the rows beside it; its caller names it.
function SidebarInput({ className, ...props }: React.ComponentProps<typeof Input>) {
  return (
    <Input
      data-slot="sidebar-input"
      className={cn("h-8 w-full bg-background shadow-none", className)}
      {...props}
    />
  );
}

function SidebarSeparator({ className, ...props }: React.ComponentProps<typeof Separator>) {
  return (
    <Separator
      data-slot="sidebar-separator"
      className={cn("mx-3 w-auto bg-sidebar-border", className)}
      {...props}
    />
  );
}

// SidebarContent is the sidebar's scrolling middle, between header and
// footer.
function SidebarContent({ className, ...props }: React.ComponentProps<"div">) {
  // Scrolling past its end stops there, so in the drawer the page behind
  // never moves: the part of React Aria's iOS scroll lock the
  // Content-Security-Policy blocks (web/README.md). Its edge fades while
  // more is there to scroll (scroll-fade-y, styles.css).
  return (
    <div
      data-slot="sidebar-content"
      className={cn(
        "flex min-h-0 flex-1 scroll-fade-y flex-col gap-0 overflow-auto overscroll-contain group-data-[collapsible=icon]:overflow-hidden",
        className,
      )}
      {...props}
    />
  );
}

function SidebarGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-group"
      className={cn("relative flex w-full min-w-0 flex-col px-3 py-2", className)}
      {...props}
    />
  );
}

// SidebarGroupLabel names a group of items, e.g. "This inbok". On the rail
// its words give way to a hairline of the same height, so the items below
// keep their place; screen readers still read the words. Forced colours
// would show the clear words, so there they take the background colour.
function SidebarGroupLabel({
  className,
  elementType: Element = "div",
  ...props
}: React.HTMLAttributes<HTMLElement> & { elementType?: React.ElementType }) {
  return (
    <Element
      data-slot="sidebar-group-label"
      className={cn(
        "relative flex h-8 shrink-0 items-center overflow-hidden px-2 text-xs whitespace-nowrap text-muted-foreground group-data-[collapsible=icon]:text-transparent group-data-[collapsible=icon]:before:absolute group-data-[collapsible=icon]:before:inset-x-2 group-data-[collapsible=icon]:before:top-1/2 group-data-[collapsible=icon]:before:h-px group-data-[collapsible=icon]:before:bg-sidebar-border forced-colors:group-data-[collapsible=icon]:text-[Canvas] forced-colors:group-data-[collapsible=icon]:forced-color-adjust-none forced-colors:group-data-[collapsible=icon]:before:bg-[CanvasText] [&>svg]:size-4 [&>svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

// SidebarGroupAction is a small button at the end of a group's label, e.g.
// "Add label", named by aria-label. It hides on the rail with the label's
// words; a coarse pointer gets a larger hit area around it.
function SidebarGroupAction({
  className,
  ...props
}: Omit<ButtonProps, "className" | "ref"> & {
  className?: string;
}) {
  return (
    <ButtonPrimitive
      data-slot="sidebar-group-action"
      className={cn(
        "absolute top-3.5 right-3 flex aspect-square w-5 items-center justify-center rounded-md p-0 text-sidebar-foreground outline-none group-data-[collapsible=icon]:hidden after:absolute after:-inset-3 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground data-focus-visible:focus-ring pointer-fine:after:hidden [&>svg]:size-4 [&>svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

function SidebarGroupContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div data-slot="sidebar-group-content" className={cn("w-full text-sm", className)} {...props} />
  );
}

// SidebarMenu lists items 2 px apart (DESIGN.md "Spacing").
function SidebarMenu({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ul
      data-slot="sidebar-menu"
      className={cn("flex w-full min-w-0 flex-col gap-0.5", className)}
      {...props}
    />
  );
}

function SidebarMenuItem({ className, ...props }: React.ComponentProps<"li">) {
  return (
    <li
      data-slot="sidebar-menu-item"
      className={cn("group/menu-item relative", className)}
      {...props}
    />
  );
}

// The open item's bar: 2 px in the text colour at its left edge, so it
// stands out from a hovered row without bold, which means unread. Forced
// colours, which blank fills and so the tint, keep it in the text colour.
const activeBar =
  "data-active:before:absolute data-active:before:inset-y-1.5 data-active:before:left-0 data-active:before:w-0.5 data-active:before:rounded-full data-active:before:bg-foreground forced-colors:data-active:before:bg-[CanvasText] forced-colors:data-active:before:forced-color-adjust-none";

// Rows are 32 px for a fine pointer, 44 px for a coarse one (DESIGN.md
// "Control height"), on the rail too, where only their icons show. The
// icon keeps its place open or as a rail: 8 px in (14 px coarse), the
// rail's centre. The open item keeps its tint while hovered; the current
// one of a set that is not a page, e.g. the account filtering it, has the
// bar alone, so the page stands out from its scope (DESIGN.md "Sidebar").
// The name
// gives way, cut short with an ellipsis, to what follows it in the row (a
// SidebarMenuBadge) and to a SidebarMenuAction pinned at the end.
const sidebarMenuButtonClasses = `group/menu-button relative flex h-8 w-full items-center gap-2 overflow-hidden rounded-md p-2 text-left text-sm font-medium transition-colors outline-none group-has-data-[slot=sidebar-menu-action]/menu-item:pr-8 group-data-[collapsible=icon]:size-8! group-data-[collapsible=icon]:p-2! hover:bg-sidebar-accent hover:text-sidebar-accent-foreground data-focus-visible:focus-ring pointer-coarse:px-3.5 pointer-coarse:group-data-[collapsible=icon]:size-11! pointer-coarse:group-data-[collapsible=icon]:p-3.5! data-disabled:pointer-events-none data-disabled:opacity-50 pointer-coarse:h-11 data-active:bg-selection data-active:hover:bg-selection data-active:aria-[current=true]:bg-transparent data-active:aria-[current=true]:hover:bg-sidebar-accent ${activeBar} data-active:text-foreground [&_svg]:size-4 [&_svg]:shrink-0 [&>span:not([data-slot])]:truncate`;

// SidebarMenuButton is one item: a link when it has an href, else a
// button. isActive marks a link as the current one: its page, or with
// current "true", the current one of a set that is not a page, e.g. an
// account filtering the page. On the rail, tooltip names it; open,
// detail, when given, says more about it on hover or focus, e.g. how far
// an account's sync has come.
function SidebarMenuButton({
  isActive = false,
  current = "page",
  tooltip,
  detail,
  className,
  ...props
}: SidebarButtonProps & {
  current?: "page" | "true";
  tooltip?: string;
  detail?: string;
  className?: string;
}) {
  const { rail } = useSidebar();
  const classes = cn(sidebarMenuButtonClasses, className);
  const comp =
    props.href === undefined ? (
      <ButtonPrimitive data-slot="sidebar-menu-button" className={classes} {...props} />
    ) : (
      <LinkPrimitive
        data-slot="sidebar-menu-button"
        data-active={isActive || undefined}
        aria-current={isActive ? current : undefined}
        className={classes}
        {...props}
      />
    );

  const tip = rail ? tooltip : detail;
  if (!tooltip && !detail) {
    return comp;
  }

  return (
    <TooltipTrigger isDisabled={!tip}>
      {comp}
      <Tooltip placement="right">{tip}</Tooltip>
    </TooltipTrigger>
  );
}

// SidebarMenuAction is a small button at the end of an item, e.g. More,
// 24 px (DESIGN.md "Control height"), centred in the row; a coarse
// pointer gets a larger hit area around it.
function SidebarMenuAction({
  className,
  ...props
}: Omit<ButtonProps, "className" | "ref"> & {
  className?: string;
}) {
  return (
    <ButtonPrimitive
      data-slot="sidebar-menu-action"
      className={cn(
        "absolute top-1 right-1 flex aspect-square w-6 items-center justify-center rounded-md p-0 text-sidebar-foreground outline-none group-data-[collapsible=icon]:hidden after:absolute after:-inset-3 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground data-focus-visible:focus-ring pointer-coarse:top-2.5 pointer-fine:after:hidden [&>svg]:size-4 [&>svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

// SidebarMenuBadge is what follows an item's name inside its
// SidebarMenuButton, e.g. its unread count, in the weight that means
// unread. It takes the width it needs and the name gives way, so nothing
// overlaps; status words, e.g. "Sync failed", follow the name the same
// way. It is part of the item's name for a screen reader, so a bare count
// needs words beside it, e.g. a sr-only "unread".
function SidebarMenuBadge({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="sidebar-menu-badge"
      className={cn(
        "ml-auto flex h-5 min-w-5 shrink-0 items-center justify-center rounded-md px-1 text-xs font-semibold tabular-nums group-data-[collapsible=icon]:hidden",
        className,
      )}
      {...props}
    />
  );
}

function SidebarMenuSkeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-menu-skeleton"
      className={cn("flex h-8 items-center gap-2 rounded-md px-2 pointer-coarse:h-11", className)}
      {...props}
    >
      <Skeleton className="size-4 rounded-md" />
      <Skeleton className="h-4 flex-1" />
    </div>
  );
}

function SidebarMenuSub({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ul
      data-slot="sidebar-menu-sub"
      className={cn(
        "mx-3.5 flex min-w-0 translate-x-px flex-col gap-0.5 border-l border-sidebar-border px-2.5 py-0.5 group-data-[collapsible=icon]:hidden",
        className,
      )}
      {...props}
    />
  );
}

function SidebarMenuSubItem({ className, ...props }: React.ComponentProps<"li">) {
  return (
    <li
      data-slot="sidebar-menu-sub-item"
      className={cn("group/menu-sub-item relative", className)}
      {...props}
    />
  );
}

// SidebarMenuSubButton is one item under another, e.g. a label under its
// account: a link when it has an href, else a button. isActive marks a
// link's page as the current one; detail, when given, says more about it
// on hover or focus, e.g. a name cut short in full.
function SidebarMenuSubButton({
  isActive = false,
  detail,
  className,
  ...props
}: SidebarButtonProps & {
  detail?: string;
  className?: string;
}) {
  const classes = cn(
    `relative flex h-8 min-w-0 -translate-x-px items-center gap-2 overflow-hidden rounded-md px-2 text-sm font-medium text-sidebar-foreground transition-colors outline-none group-data-[collapsible=icon]:hidden hover:bg-sidebar-accent hover:text-sidebar-accent-foreground data-focus-visible:focus-ring pointer-coarse:h-11 data-disabled:pointer-events-none data-disabled:opacity-50 data-active:bg-selection data-active:hover:bg-selection ${activeBar} data-active:text-foreground [&>span:not([data-slot])]:truncate [&>svg]:size-4 [&>svg]:shrink-0`,
    className,
  );
  const comp =
    props.href === undefined ? (
      <ButtonPrimitive data-slot="sidebar-menu-sub-button" className={classes} {...props} />
    ) : (
      <LinkPrimitive
        data-slot="sidebar-menu-sub-button"
        data-active={isActive || undefined}
        aria-current={isActive ? "page" : undefined}
        className={classes}
        {...props}
      />
    );
  if (!detail) return comp;
  return (
    <TooltipTrigger>
      {comp}
      <Tooltip placement="right">{detail}</Tooltip>
    </TooltipTrigger>
  );
}

export {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInput,
  SidebarInset,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarSeparator,
  SidebarTrigger,
  useSidebar,
};
