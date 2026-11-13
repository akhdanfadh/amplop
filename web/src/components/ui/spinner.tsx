import { cn } from "@/lib/cn";
import { Loader2Icon } from "lucide-react";

// Spinner turns while work is running. It is decoration: the words beside
// it, e.g. "Loading...", say what runs, in a status region of the caller's.
function Spinner({ className, ...props }: React.ComponentProps<"svg">) {
  return (
    <Loader2Icon
      data-slot="spinner"
      aria-hidden="true"
      className={cn("size-4 animate-spin motion-reduce:animate-none", className)}
      {...props}
    />
  );
}

export { Spinner };
