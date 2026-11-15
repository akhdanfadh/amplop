import type { ReactElement } from "react";
import { UserIcon } from "lucide-react";
import type { MailAddressOutput } from "@/api/generated/model";
import { accountRing, type AccountColor } from "@/components/account-mark";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { cn } from "@/lib/cn";
import { initials } from "./people";

// SenderAvatar shows sender's initials in a 36 px circle, or a person icon
// when it has none (DESIGN.md "Message list", "Reading pane"). ring, an
// account's colour, rings it inside its edge; without one it keeps its
// hairline edge, which marks the muted disc on the page.
export function SenderAvatar({
  sender,
  ring,
}: {
  sender: MailAddressOutput | undefined;
  ring?: AccountColor;
}): ReactElement {
  const letters = initials(sender);
  return (
    <Avatar
      className={cn(
        "size-9",
        ring !== undefined && ["after:border-2 after:mix-blend-normal", accountRing(ring)],
      )}
    >
      <AvatarFallback className="text-xs font-semibold">
        {letters || <UserIcon aria-hidden="true" className="size-4" />}
      </AvatarFallback>
    </Avatar>
  );
}
