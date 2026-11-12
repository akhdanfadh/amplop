import type * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "cn";
import {
  Button as ButtonPrimitive,
  type ButtonProps as ButtonPrimitiveProps,
} from "react-aria-components";

// Heights follow DESIGN.md "Control height": 36 px for a fine pointer,
// 44 px for a coarse one. The focus ring follows React Aria's
// data-focus-visible, which stays off after a mouse press (shadcn-ui/ui#11258);
// the same classes style a router link (no data-rac) by :focus-visible.
// Tailwind's hover: applies only where a pointer can hover.
const buttonVariants = cva(
  "group/button inline-flex shrink-0 items-center justify-center rounded-lg border border-transparent bg-clip-padding text-sm font-medium whitespace-nowrap transition-colors outline-none select-none not-data-rac:focus-visible:focus-ring aria-invalid:border-destructive data-focus-visible:focus-ring data-pending:cursor-progress data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        outline: "border-input bg-card hover:bg-accent aria-expanded:bg-accent dark:bg-transparent",
        ghost: "hover:bg-accent aria-expanded:bg-accent",
        destructive:
          "bg-destructive/8 text-destructive hover:bg-destructive/12 dark:bg-destructive/12 dark:hover:bg-destructive/16",
        link: "text-primary underline underline-offset-4 hover:decoration-2",
      },
      size: {
        // A minimum, not a height, so a page can let a long label wrap
        // with whitespace-normal.
        default:
          "min-h-9 gap-1.5 px-3 py-1.5 has-data-[icon=inline-end]:pr-2.5 has-data-[icon=inline-start]:pl-2.5 pointer-coarse:min-h-11",
        sm: "min-h-8 gap-1 px-2.5 py-1 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 pointer-coarse:min-h-11",
        icon: "size-9 pointer-coarse:size-11",
        "icon-sm": "size-8 pointer-coarse:size-11",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

type ButtonProps = Omit<ButtonPrimitiveProps, "className"> &
  React.RefAttributes<HTMLButtonElement> &
  VariantProps<typeof buttonVariants> & {
    className?: string;
  };

// Button is React Aria's button: isDisabled takes it out of the tab order,
// isPending keeps it focusable while it blocks presses and is announced.
function Button({ className, variant = "default", size = "default", ...props }: ButtonProps) {
  return (
    <ButtonPrimitive
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  );
}

export { Button, buttonVariants, type ButtonProps };
