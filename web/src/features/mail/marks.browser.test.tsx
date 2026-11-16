import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Marked } from "./marks";

// marked returns the marked parts of text, as Marked draws them.
async function marked(text: string, spans: { start: number; length: number }[]) {
  const { container } = await render(
    <p>
      <Marked text={text} spans={spans} />
    </p>,
  );
  await expect.element(page.getByText(text.slice(0, 1), { exact: false })).toBeVisible();
  return {
    marks: [...container.querySelectorAll("mark")].map((mark) => mark.textContent),
    text: container.textContent,
  };
}

describe("Marked", () => {
  it("marks each span, counting as JavaScript strings do", async () => {
    // The emoji takes two UTF-16 units, as the API counts it.
    expect(await marked("😀 Kalibata lease", [{ start: 3, length: 8 }])).toEqual({
      marks: ["Kalibata"],
      text: "😀 Kalibata lease",
    });
  });

  it("skips spans out of order, overlapping, or past the end", async () => {
    expect(
      await marked("one two three", [
        { start: 4, length: 3 },
        { start: 0, length: 3 },
        { start: 5, length: 4 },
        { start: 8, length: 20 },
      ]),
    ).toEqual({ marks: ["two"], text: "one two three" });
  });
});
