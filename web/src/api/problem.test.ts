import { describe, expect, it } from "vite-plus/test";
import { summarize } from "./problem";
import { ApiError } from "./transport";

describe("summarize", () => {
  it("joins the title and detail", () => {
    const err = new ApiError("status", {
      status: 404,
      title: "Not Found",
      detail: "route not found",
    });

    expect(summarize(err)).toBe("Not Found: route not found");
  });

  it("shows the title alone without a detail", () => {
    const err = new ApiError("status", { status: 502, title: "Bad Gateway" });

    expect(summarize(err)).toBe("Bad Gateway");
  });
});
