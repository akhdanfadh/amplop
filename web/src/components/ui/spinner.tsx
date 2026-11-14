import { cn } from "@/lib/cn";
import { Loader2Icon } from "lucide-react";

// Spinner turns while work is running. It is decoration: the words beside
// it, e.g. "Loading...", say what runs, in a status region of the caller's.
// With reduced motion it fades in and out instead of turning: a still arc
// reads as a broken icon, and some spinners have no words beside them,
// e.g. a list's row for its next page.
function Spinner({ className, ...props }: React.ComponentProps<"svg">) {
  return (
    <Loader2Icon
      data-slot="spinner"
      aria-hidden="true"
      className={cn("size-4 animate-spin motion-reduce:animate-pulse", className)}
      {...props}
    />
  );
}

export { Spinner };
