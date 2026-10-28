import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { sessionMessage, uncertainSession } from "./session-error";

const httpStatus = { created: 201, unauthorized: 401, internalServerError: 500 };

describe("session request errors", () => {
  it("offers a session check when the outcome is unknown", () => {
    for (const err of [
      new ApiError("network"),
      new ApiError("timeout"),
      new ApiError("http", { status: httpStatus.internalServerError }),
      new ApiError("response", { status: httpStatus.created }),
    ]) {
      expect(uncertainSession(err)).toBe(true);
      expect(sessionMessage(err, "sign-in")).toContain("Could not confirm sign-in.");
      expect(sessionMessage(err, "sign-out")).toContain("Could not confirm sign-out.");
      expect(sessionMessage(err, "sign-in")).not.toContain("saved");
    }
  });

  it("keeps a received rejection separate from an unknown outcome", () => {
    const err = new ApiError("network", {
      status: httpStatus.unauthorized,
      problem: { detail: "The email or password is incorrect." },
    });
    expect(uncertainSession(err)).toBe(false);
    expect(sessionMessage(err, "sign-in")).toBe("The email or password is incorrect.");
  });
});
