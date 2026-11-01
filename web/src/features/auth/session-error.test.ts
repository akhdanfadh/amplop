import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { sessionMessage } from "./session-error";

const httpStatus = { created: 201, unauthorized: 401, internalServerError: 500 };

describe("session request errors", () => {
  it.each([
    new ApiError("network", { method: "POST" }),
    new ApiError("timeout", { method: "POST" }),
    new ApiError("http", { method: "POST", status: httpStatus.internalServerError }),
    new ApiError("response", { method: "POST", status: httpStatus.created }),
  ])("offers a session check when the outcome is unknown", (err) => {
    expect(sessionMessage(err, "sign-in")).toContain("Could not confirm sign-in.");
    expect(sessionMessage(err, "sign-out")).toContain("Could not confirm sign-out.");
    expect(sessionMessage(err, "sign-in")).not.toContain("saved");
  });

  it("keeps a received rejection separate from an unknown outcome", () => {
    const err = new ApiError("network", {
      method: "POST",
      status: httpStatus.unauthorized,
      problem: { detail: "The email or password is incorrect." },
    });

    expect(sessionMessage(err, "sign-in")).toBe("The email or password is incorrect.");
  });

  it("puts the API's explanation before the session check", () => {
    const err = new ApiError("http", {
      method: "DELETE",
      status: httpStatus.internalServerError,
      problem: { title: "Internal error", detail: "Something went wrong on our side." },
    });

    expect(sessionMessage(err, "sign-out")).toBe(
      "Something went wrong on our side. Could not confirm sign-out. Check your session before trying again.",
    );
  });
});
