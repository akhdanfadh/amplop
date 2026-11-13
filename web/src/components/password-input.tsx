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
    <InputGroup>
      <InputGroupInput {...props} type={visible ? "text" : "password"} />
      <InputGroupAddon align="inline-end" className="py-0">
        <InputGroupButton
          size="icon-sm"
          aria-label={`${visible ? "Hide" : "Show"} ${label}`}
          aria-controls={props.id}
          isDisabled={props.disabled}
          onPress={() => setVisible((previous) => !previous)}
        >
          <Icon aria-hidden="true" data-icon="inline-end" />
        </InputGroupButton>
      </InputGroupAddon>
    </InputGroup>
  );
}
