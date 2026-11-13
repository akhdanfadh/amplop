import { CheckIcon } from "lucide-react";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { StatusBadge, type Tone } from "./status-badge";

// Each tone's text colour in the light theme, from DESIGN.md "Colour".
const lightColours: [Tone, string][] = [
  ["neutral", "rgb(10, 10, 10)"],
  ["info", "rgb(37, 87, 214)"],
  ["success", "rgb(15, 102, 48)"],
  ["warning", "rgb(144, 69, 0)"],
  ["destructive", "rgb(168, 0, 10)"],
];
const tones = lightColours.map(([tone]) => tone);

// WCAG 2.2's minimum contrast for normal text (success criterion 1.4.3).
const minimumContrast = 4.5;

type Rgb = [number, number, number];

// painted returns the sRGB channels of colours painted over each other in
// order, the first opaque, as the screen shows them.
function painted(...colours: string[]): Rgb {
  const context = document.createElement("canvas").getContext("2d");
  if (!context) throw new Error("No 2D canvas");
  for (const colour of colours) {
    context.fillStyle = colour;
    context.fillRect(0, 0, 1, 1);
  }
  const [r = 0, g = 0, b = 0] = context.getImageData(0, 0, 1, 1).data;
  return [r, g, b];
}

// luminance returns the WCAG 2.2 relative luminance of an sRGB colour.
function luminance(colour: Rgb): number {
  const [r = 0, g = 0, b = 0] = colour.map((channel) => {
    const c = channel / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

// contrast returns the WCAG 2.2 contrast ratio of two sRGB colours.
function contrast(first: Rgb, second: Rgb): number {
  const [x, y] = [luminance(first), luminance(second)];
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

afterEach(() => {
  document.documentElement.classList.remove("dark");
});

describe("StatusBadge", () => {
  it("shows a word with an icon that screen readers skip", async () => {
    await render(
      <StatusBadge tone="success" icon={CheckIcon}>
        Sent
      </StatusBadge>,
    );
    const badge = page.getByText("Sent").element();

    expect(badge.textContent).toBe("Sent");
    expect(badge.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("colours each tone with its token", async () => {
    await render(
      <>
        {tones.map((tone) => (
          <StatusBadge key={tone} tone={tone} icon={CheckIcon}>
            {tone}
          </StatusBadge>
        ))}
      </>,
    );

    for (const [tone, colour] of lightColours) {
      expect(getComputedStyle(page.getByText(tone).element()).color).toBe(colour);
    }
  });

  it.each(["light", "dark"])("keeps each word readable on its tint, %s", async (theme) => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    await render(
      <div className="bg-background">
        {tones.map((tone) => (
          <StatusBadge key={tone} tone={tone} icon={CheckIcon}>
            {tone}
          </StatusBadge>
        ))}
      </div>,
    );

    const unreadable = tones.filter((tone) => {
      const element = page.getByText(tone).element();
      const badge = getComputedStyle(element);
      const surface = getComputedStyle(element.parentElement ?? document.body).backgroundColor;
      const tint = painted(surface, badge.backgroundColor);
      const text = painted(surface, badge.backgroundColor, badge.color);
      return contrast(text, tint) < minimumContrast;
    });
    expect(unreadable).toEqual([]);
  });
});
