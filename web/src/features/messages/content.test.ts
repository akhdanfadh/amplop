import { describe, expect, it } from "vite-plus/test";
import { toContent } from "./content";

describe("toContent", () => {
  it("shows the API's bodies and files, a clipped text with its whole", () => {
    expect(
      toContent({
        remote: "blocked",
        bodies: [
          {
            type: "html",
            partId: "0.0",
            url: "/api/v0/messages/m/body/0.0",
            remoteUrl: "/api/v0/messages/m/body/0.0?remote=allowed",
            text: null,
            clipped: false,
          },
          {
            type: "text",
            partId: "0.1",
            url: "/api/v0/messages/m/parts/0.1",
            remoteUrl: null,
            text: "Start of a long text",
            clipped: true,
          },
        ],
        attachments: [
          {
            partId: "0.2",
            filename: "a.pdf",
            mediaType: "application/pdf",
            size: 3,
            url: "/api/v0/messages/m/parts/0.2",
          },
        ],
      }),
    ).toEqual({
      status: "success",
      remote: "blocked",
      bodies: [
        {
          partId: "0.0",
          type: "html",
          url: "/api/v0/messages/m/body/0.0",
          remoteUrl: "/api/v0/messages/m/body/0.0?remote=allowed",
        },
        {
          partId: "0.1",
          type: "text",
          text: "Start of a long text",
          wholeUrl: "/api/v0/messages/m/parts/0.1",
        },
      ],
      attachments: [
        {
          partId: "0.2",
          filename: "a.pdf",
          mediaType: "application/pdf",
          size: 3,
          url: "/api/v0/messages/m/parts/0.2",
        },
      ],
    });
  });

  it("leaves out a body or file whose address is not the API's", () => {
    const content = toContent({
      remote: "none",
      bodies: [
        {
          type: "html",
          partId: "0",
          url: "https://example.com/x",
          remoteUrl: null,
          text: null,
          clipped: false,
        },
      ],
      attachments: [
        { partId: "1", filename: "a", mediaType: "", size: 1, url: "javascript:alert(1)" },
      ],
    });

    expect(content).toEqual({ status: "success", remote: "none", bodies: [], attachments: [] });
  });
});
