import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { ApiError } from "@/api/transport";
import { messagePath } from "@/test/message-server";
import { Attachments, type Attachment } from "./attachments";
import { HtmlBody } from "./html-body";
import { MessageContentView, type MessageContent } from "./view";

// loaded is content that has loaded with the bodies and attachments given.
function loaded(content: Partial<Extract<MessageContent, { status: "success" }>>): MessageContent {
  return { status: "success", bodies: [], remote: "none", attachments: [], ...content };
}

// frames returns the message's body frames.
function frames(): HTMLIFrameElement[] {
  return [...document.querySelectorAll("iframe")];
}

afterEach(async () => {
  await page.viewport(414, 896);
});

describe("MessageContentView", () => {
  it("shows images from the web only once the member asks", async () => {
    await render(
      <MessageContentView
        content={loaded({
          remote: "blocked",
          bodies: [
            {
              partId: "0",
              type: "html",
              url: `${messagePath}fluid?remote=blocked`,
              remoteUrl: `${messagePath}fluid?remote=allowed`,
            },
          ],
        })}
      />,
    );
    await expect.element(page.getByText(/inbok hides images from the web/)).toBeVisible();
    expect(frames().map((frame) => frame.src)).toEqual([expect.stringContaining("remote=blocked")]);

    await userEvent.click(page.getByRole("button", { name: "Show images" }));

    // The notice says so and takes focus, as its button went away.
    const notice = page.getByRole("note");
    await expect
      .element(notice.getByText("Showing images from the web for this message."))
      .toBeVisible();
    await expect.element(notice).toHaveFocus();
    expect(frames().map((frame) => frame.src)).toEqual([expect.stringContaining("remote=allowed")]);
  });

  it("offers attachments as downloads with their names and sizes", async () => {
    await render(
      <MessageContentView
        content={loaded({
          attachments: [
            {
              partId: "0.1",
              filename: "Budget 2027.xlsx",
              mediaType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
              size: 52_000,
              url: "/api/v0/messages/m/parts/0.1",
            },
            {
              partId: "0.2",
              filename: "",
              mediaType: "application/octet-stream",
              size: 300,
              url: "/api/v0/messages/m/parts/0.2",
            },
          ],
        })}
      />,
    );
    const list = page.getByRole("list", { name: "Attachments" });
    const named = list.getByRole("link", { name: "Budget 2027.xlsx 51 KB" });
    const unnamed = list.getByRole("link", { name: "Unnamed file 300 bytes" });

    await expect.element(named).toHaveAttribute("href", "/api/v0/messages/m/parts/0.1");
    await expect.element(named).toHaveAttribute("download", "Budget 2027.xlsx");
    // An empty download attribute takes the server's name for the file.
    await expect.element(unnamed).toHaveAttribute("download", "");
  });

  it("offers the whole of a long text body as a download", async () => {
    await render(
      <MessageContentView
        content={loaded({
          bodies: [{ partId: "0", type: "text", text: "The start", wholeUrl: "/whole" }],
        })}
      />,
    );
    const link = page.getByRole("link", { name: "Download the whole text" });

    await expect.element(page.getByText(/inbok shows only its start/)).toBeVisible();
    await expect.element(link).toHaveAttribute("href", "/whole");
    await expect.element(link).toHaveAttribute("download", "message.txt");
  });

  it("says when content failed to load, and tries again", async () => {
    const onRetry = vi.fn<() => void>();
    await render(
      <MessageContentView
        content={{
          status: "error",
          error: new ApiError("http", { method: "GET", status: 502 }),
          retrying: false,
          onRetry,
        }}
      />,
    );

    await expect
      .element(page.getByRole("alert").getByText(/^Could not load this message\./))
      .toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it("fits a phone's width without scrolling the page sideways", async () => {
    await page.viewport(375, 812);
    await render(
      <MessageContentView
        content={loaded({
          bodies: [
            {
              partId: "0",
              type: "html",
              url: `${messagePath}wide600`,
              remoteUrl: `${messagePath}wide600`,
            },
          ],
          attachments: [
            {
              partId: "0.1",
              filename: `${"long-name-".repeat(12)}.pdf`,
              mediaType: "application/pdf",
              size: 1,
              url: "/api/v0/messages/m/parts/0.1",
            },
          ],
        })}
      />,
    );
    // The body is shown, scaled to the frame.
    await expect
      .poll(() => frames()[0]?.contentDocument?.documentElement.style.getPropertyValue("zoom"))
      .not.toBe("");

    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);
  });
});

// part is attachment i, a PDF named filename, offered at an API path.
function part(i: number, filename: string): Attachment {
  return {
    partId: `0.${i}`,
    filename,
    mediaType: "application/pdf",
    size: 52_000,
    url: `/api/v0/messages/m/parts/0.${i}`,
  };
}

describe("Attachments", () => {
  it("cuts long names in the middle, so similar files stay apart", async () => {
    await page.viewport(375, 812);
    await render(
      <Attachments
        attachments={[
          part(1, "Q3-report-northwind-industries-holdings-final-v11.pdf"),
          part(2, "Q3-report-northwind-industries-holdings-final-v12.pdf"),
        ]}
      />,
    );
    const v11 = page.getByRole("link", { name: /final-v11\.pdf/ });
    const v12 = page.getByRole("link", { name: /final-v12\.pdf/ });

    // Screen readers hear each name whole.
    await expect
      .element(v11)
      .toHaveAccessibleName("Q3-report-northwind-industries-holdings-final-v11.pdf 51 KB");
    // The shown text ends differently, while its start is cut.
    expect(v11.element().textContent).not.toBe(v12.element().textContent);
    const start = v11.element().querySelector("bdi span");
    expect(start && start.scrollWidth > start.clientWidth).toBe(true);
  });

  it("shows a cut name whole in a tooltip, and a short one never", async () => {
    await render(
      <Attachments
        attachments={[
          part(
            1,
            "Laporan Keuangan Konsolidasi PT Nusantara Sejahtera Abadi Tbk Kuartal III 2026.pdf",
          ),
          part(2, "a.pdf"),
        ]}
      />,
    );

    await userEvent.keyboard("{Tab}");
    await expect
      .element(page.getByRole("tooltip"))
      .toHaveTextContent(
        "Laporan Keuangan Konsolidasi PT Nusantara Sejahtera Abadi Tbk Kuartal III 2026.pdf",
      );
    await userEvent.keyboard("{Tab}");
    await expect.element(page.getByRole("link", { name: /^a\.pdf/ })).toHaveFocus();
    await expect.element(page.getByRole("tooltip")).not.toBeInTheDocument();
  });
});

// isCutAnywhere reports whether a part of link's shown name is cut.
function isCutAnywhere(link: Element): boolean {
  const pieces = Array.from(link.querySelector("bdi")?.children ?? []);
  return pieces.some((piece) => piece.scrollWidth > piece.clientWidth);
}

describe("Attachments at their limits", () => {
  it("keeps every extension whole at twice the text size", async () => {
    await page.viewport(375, 812);
    // Rem sizes, as a member's larger default text sets them.
    document.documentElement.style.fontSize = "200%";
    try {
      await render(
        <Attachments
          attachments={[
            part(1, "Q3-report-northwind-industries-holdings-final-v11.xlsx"),
            part(2, "تقرير الربع الثالث النهائي للشركة القابضة.pdf"),
          ]}
        />,
      );

      for (const [link, extension] of [
        [page.getByRole("link", { name: /v11/ }), ".xlsx"],
        [page.getByRole("link", { name: /تقرير/ }), ".pdf"],
      ] as const) {
        const shown = link.element().querySelector("bdi")?.lastElementChild;
        expect(shown?.textContent).toBe(extension);
        expect(shown && shown.scrollWidth <= shown.clientWidth).toBe(true);
        // The parts before it give way instead.
        expect(isCutAnywhere(link.element())).toBe(true);
      }
    } finally {
      document.documentElement.style.fontSize = "";
    }
  });

  it("wraps a cut name with no break points inside its tooltip", async () => {
    await page.viewport(375, 812);
    const name = `IMG_${"20261113_101530123_".repeat(8)}HDR.jpg`;
    await render(<Attachments attachments={[part(1, name)]} />);

    await userEvent.keyboard("{Tab}");
    const tooltip = page.getByRole("tooltip");
    await expect.element(tooltip).toBeVisible();

    const box = tooltip.element().getBoundingClientRect();
    expect(box.right).toBeLessThanOrEqual(375);
    // The text stays inside the tooltip's own box.
    expect(tooltip.element().scrollWidth).toBeLessThanOrEqual(tooltip.element().clientWidth);
  });

  it("keeps a wide name's end inside the chip, clear of its size", async () => {
    await render(<Attachments attachments={[part(1, `report-${"W".repeat(30)}.WWWWWWWWW`)]} />);
    const link = page.getByRole("link").element();
    const size = link.lastElementChild;
    const end = link.querySelector("bdi")?.lastElementChild;
    if (!size || !end) throw new Error("the chip has no size or name end");

    expect(end.getBoundingClientRect().right).toBeLessThanOrEqual(
      size.getBoundingClientRect().left,
    );
    expect(link.scrollWidth).toBeLessThanOrEqual(link.clientWidth);
  });
});

describe("Bodies", () => {
  it("lays a text body out in its own direction", async () => {
    await render(
      <MessageContentView
        content={loaded({
          bodies: [{ partId: "0", type: "text", text: "مرحبا، هذا تقرير الربع الثالث." }],
        })}
      />,
    );

    const body = page.getByText("مرحبا، هذا تقرير الربع الثالث.");
    await expect.element(body).toHaveAttribute("dir", "auto");
    expect(getComputedStyle(body.element()).direction).toBe("rtl");
  });

  it("says when a message has no text", async () => {
    await render(
      <MessageContentView
        content={loaded({ bodies: [{ partId: "0", type: "text", text: "  \n" }] })}
      />,
    );

    await expect.element(page.getByText("This message has no text.")).toBeVisible();
  });
});

describe("HtmlBody", () => {
  it("shows a body that loads on Try again, and gives it focus", async () => {
    await render(
      <HtmlBody url={`${messagePath}flaky?key=${crypto.randomUUID()}`} title="Message body" />,
    );
    const retry = page.getByRole("button", { name: "Try again" });
    await expect
      .element(page.getByRole("alert").getByText("Could not show this part of the message."))
      .toBeVisible();

    await userEvent.click(retry);

    // The button went away; focus goes to the body that replaced it.
    await expect.element(page.getByTitle("Message body")).toHaveFocus();
    await expect.element(page.getByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps focus on Try again when the body fails again", async () => {
    await render(<HtmlBody url={`${messagePath}error`} title="Message body" />);
    const retry = page.getByRole("button", { name: "Try again" });
    await expect.element(retry).toBeVisible();

    await userEvent.click(retry);

    await expect.element(page.getByRole("alert")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Try again" })).toHaveFocus();
  });
});
