import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { ApiError } from "@/api/transport";
import { ReadError } from "./read-error";

describe("ReadError", () => {
  it("is an alert in a page, which a screen reader reads at once", async () => {
    await render(
      <ReadError
        message="Couldn't load the outbox."
        error={new ApiError("network", { method: "GET" })}
        retrying={false}
        onRetry={() => {}}
      />,
    );
    await expect
      .element(page.getByRole("alert").getByText("Couldn't load the outbox."))
      .toBeVisible();
  });
});
