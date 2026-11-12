import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { RadioGroup, RadioGroupItem } from "./radio-group";

describe("RadioGroup", () => {
  it("names its radios after the group, so the checked one can be found", async () => {
    await render(
      <RadioGroup aria-label="Security" name="security" defaultValue="tls">
        <RadioGroupItem value="none">None</RadioGroupItem>
        <RadioGroupItem value="tls">TLS</RadioGroupItem>
      </RadioGroup>,
    );

    // The selector the system email form focuses on a Security error.
    const checked = document.querySelector('[name="security"]:checked');
    expect(checked).toBe(page.getByRole("radio", { name: "TLS" }).element());
  });
});
