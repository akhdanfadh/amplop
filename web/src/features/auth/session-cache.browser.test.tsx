import { QueryClient } from "@tanstack/react-query";
import { expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { showError, Toaster } from "@/components/ui/toast";
import { clearAccountCache } from "./session-cache";

it("closes every toast, so the next member sees none of the last one's", async () => {
  await render(<Toaster />);
  showError("Couldn't sign out");
  await expect.element(page.getByRole("alertdialog")).toBeVisible();

  clearAccountCache(new QueryClient());
  await expect.element(page.getByRole("alertdialog")).not.toBeInTheDocument();
});
