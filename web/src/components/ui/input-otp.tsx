import * as React from "react";
import { cn } from "@/lib/cn";
import { OTPInput, OTPInputContext } from "input-otp";
import { MinusIcon } from "lucide-react";
import inputOTPStyles from "./input-otp.css?url";

function InputOTP({
  className,
  containerClassName,
  ...props
}: React.ComponentProps<typeof OTPInput> & {
  containerClassName?: string;
}) {
  return (
    <>
      {/*
       * WORKAROUND, relying on input-otp internals. On mount, OTPInput adds
       * its rules in an inline <style> unless an element with the id
       * input-otp-style exists. The server's Content-Security-Policy blocks
       * that <style>: the browser logs a violation and the rules are lost.
       * This stylesheet takes the id and holds the same rules, so OTPInput
       * skips its own. React moves it into <head> once, however many inputs
       * a page has.
       *
       * The id is not documented. If an upgrade renames it, the violation
       * comes back on every page with a code input, and the copied rules in
       * input-otp.css can drift: after upgrading input-otp, compare both
       * with the library's dist/index.mjs.
       *
       * The documented way is the nonce prop, but a nonce must be new for
       * every response, so the server would rewrite the page each time.
       */}
      <link rel="stylesheet" href={inputOTPStyles} precedence="default" id="input-otp-style" />
      <OTPInput
        data-slot="input-otp"
        containerClassName={cn(
          "cn-input-otp flex items-center has-disabled:opacity-50",
          containerClassName,
        )}
        spellCheck={false}
        className={cn("disabled:cursor-not-allowed", className)}
        {...props}
      />
    </>
  );
}

function InputOTPGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="input-otp-group"
      className={cn("flex items-center rounded-lg", className)}
      {...props}
    />
  );
}

function InputOTPSlot({
  index,
  className,
  ...props
}: React.ComponentProps<"div"> & {
  index: number;
}) {
  const inputOTPContext = React.useContext(OTPInputContext);
  const { char, hasFakeCaret, isActive } = inputOTPContext?.slots[index] ?? {};

  return (
    <div
      data-slot="input-otp-slot"
      data-active={isActive}
      className={cn(
        "relative flex h-11 w-8 min-w-0 flex-1 items-center justify-center border-y border-r border-input bg-card text-base outline-none first:rounded-l-lg first:border-l last:rounded-r-lg aria-invalid:border-destructive data-[active=true]:z-10 data-[active=true]:focus-ring dark:bg-input/10",
        className,
      )}
      {...props}
    >
      {char}
      {hasFakeCaret && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <div className="h-4 w-px bg-foreground" />
        </div>
      )}
    </div>
  );
}

function InputOTPSeparator({ ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="input-otp-separator"
      className="flex items-center [&_svg:not([class*='size-'])]:size-4"
      aria-hidden="true"
      {...props}
    >
      <MinusIcon />
    </div>
  );
}

export { InputOTP, InputOTPGroup, InputOTPSlot, InputOTPSeparator };
