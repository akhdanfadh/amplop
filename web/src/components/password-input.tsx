import { useState, type ComponentProps, type ReactElement } from "react";
import { EyeIcon, EyeOffIcon } from "lucide-react";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";

// PasswordInput toggles visibility without changing the entered password.
export function PasswordInput({
  label,
  ...props
}: ComponentProps<"input"> & { label: string }): ReactElement {
  const [visible, setVisible] = useState(false);
  const Icon = visible ? EyeOffIcon : EyeIcon;

  return (
    <InputGroup className="h-auto min-h-11 border-muted-foreground has-[[data-slot=input-group-control]:focus-visible]:outline-2 has-[[data-slot=input-group-control]:focus-visible]:outline-offset-2 has-[[data-slot=input-group-control]:focus-visible]:outline-foreground has-[[data-slot=input-group-control]:focus-visible]:outline-solid">
      <InputGroupInput {...props} className="h-11" type={visible ? "text" : "password"} />
      <InputGroupAddon align="inline-end" className="py-0">
        <InputGroupButton
          className="size-11 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
          size="icon-sm"
          aria-label={`${visible ? "Hide" : "Show"} ${label}`}
          aria-controls={props.id}
          disabled={props.disabled}
          onClick={() => setVisible((previous) => !previous)}
        >
          <Icon aria-hidden="true" data-icon="inline-end" />
        </InputGroupButton>
      </InputGroupAddon>
    </InputGroup>
  );
}
