import { useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleAlertIcon, DoorClosedIcon, DoorOpenIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import {
  getGetRegistrationSettingsQueryKey,
  useSaveRegistrationSettings,
} from "@/api/generated/amplop";
import type { RegistrationSettings } from "@/api/generated/model";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { StatusBadge } from "@/components/status-badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { SettingsSection } from "@/features/settings/page";
import {
  actionWords,
  badgeText,
  offeredAction,
  saveErrorMessage,
  signUpAbout,
  signUpTitle,
  statusMessage,
  systemEmailLink,
  untestedNote,
  type RegistrationAction,
} from "./text";

const unauthorized = 401;

// RegistrationSection says whether visitors can create accounts and offers
// the one change that fits: Open, or Close, each confirmed and held in a
// dialog until the server answers. settings is the last read; the server's
// allowedActions decides what is offered, and systemEmailTested explains a
// missing Open with a way to System email.
export function RegistrationSection({
  settings,
}: {
  settings: RegistrationSettings;
}): ReactElement {
  const client = useQueryClient();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const actionsRef = useRef<HTMLDivElement>(null);
  // The change the dialog asks about, kept while it closes so its words stay.
  const [asked, setAsked] = useState<RegistrationAction>("open");
  const [open, setOpen] = useState(false);
  // Read by screen readers once a change is saved; the status shows it.
  const [announcement, setAnnouncement] = useState("");
  const save = useSaveRegistrationSettings({
    mutation: {
      // A read that started before the save could land after it and show the
      // old status.
      onMutate: () =>
        client.cancelQueries({ queryKey: getGetRegistrationSettingsQueryKey(), exact: true }),
      onSuccess: (result) => client.setQueryData(getGetRegistrationSettingsQueryKey(), result),
      // Every outcome, a lost answer included, is settled by the server's
      // settings; a refusal also means this page was out of date, e.g.
      // system email changed in another tab.
      onSettled: () =>
        void client.invalidateQueries({ queryKey: getGetRegistrationSettingsQueryKey() }),
    },
  });
  const action = offeredAction(settings);
  const blocked = settings.status === "closed" && !action && !settings.systemEmailTested;
  const linkShown = blocked || settings.status === "paused";
  const words = actionWords(asked);
  const failure =
    save.error && save.error.status !== unauthorized
      ? { message: saveErrorMessage(save.error, asked), requestId: save.error.requestId }
      : undefined;

  // choose opens the dialog that confirms next.
  function choose(next: RegistrationAction): void {
    save.reset();
    setAsked(next);
    setOpen(true);
  }

  // confirm saves the change the dialog asks about.
  function confirm(): void {
    if (save.isPending) return;
    setAnnouncement("");
    save.mutate(
      { data: { open: asked === "open" } },
      {
        onSuccess: () => {
          setOpen(false);
          setAnnouncement(actionWords(asked).saved);
        },
      },
    );
  }

  return (
    <SettingsSection title={signUpTitle} about={signUpAbout} headingRef={headingRef}>
      <p>{statusMessage(settings.status)}</p>
      {blocked && <p className="text-muted-foreground">{untestedNote}</p>}
      {(action || linkShown) && (
        <div ref={actionsRef} className="flex flex-wrap gap-3">
          {action && (
            <Button
              type="button"
              variant={action === "open" ? "default" : "outline"}
              className="whitespace-normal"
              onPress={() => choose(action)}
            >
              {actionWords(action).action}
            </Button>
          )}
          {linkShown && (
            <Link
              to="/settings/system-email"
              className={cn(
                // A passing test is what opens or resumes registration.
                buttonVariants({ variant: "default" }),
                "whitespace-normal",
              )}
            >
              {systemEmailLink}
            </Link>
          )}
        </div>
      )}
      <output aria-atomic="true" className="sr-only">
        {announcement}
      </output>
      <ConfirmDialog
        isOpen={open}
        onOpenChange={setOpen}
        title={words.title}
        description={words.description}
        action={words.action}
        working={words.working}
        tone={asked === "close" ? "destructive" : "default"}
        pending={save.isPending}
        failure={failure}
        onConfirm={confirm}
        // Closing a paused registration whose email is untested leaves no
        // Close button: the way to System email is what remains.
        fallbackFocus={() =>
          actionsRef.current?.querySelector<HTMLElement>("button, a") ?? headingRef.current
        }
      />
    </SettingsSection>
  );
}

// RegistrationBadge says whether visitors can register: Open and Closed are
// the administrator's choice, so neutral; Paused asks for a look.
export function RegistrationBadge({
  status,
}: {
  status: RegistrationSettings["status"];
}): ReactElement {
  if (status === "paused")
    return (
      <StatusBadge tone="warning" icon={CircleAlertIcon}>
        {badgeText.paused}
      </StatusBadge>
    );
  return (
    <StatusBadge tone="neutral" icon={status === "open" ? DoorOpenIcon : DoorClosedIcon}>
      {badgeText[status]}
    </StatusBadge>
  );
}
