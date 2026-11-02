import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { challengeEnded, sessionMessage } from "./session-error";

const httpStatus = {
  created: 201,
  unauthorized: 401,
  forbidden: 403,
  unprocessable: 422,
  internalServerError: 500,
};

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

  it("shows why a deactivated account cannot sign in", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: httpStatus.forbidden,
      problem: {
        type: "/api/v0/problems/account-deactivated",
        detail: "This account is deactivated. Ask your administrator.",
      },
    });

    expect(sessionMessage(err, "sign-in")).toBe(
      "This account is deactivated. Ask your administrator.",
    );
  });
});

describe("code step errors", () => {
  it("ends the challenge on a 401 or 403", () => {
    for (const status of [httpStatus.unauthorized, httpStatus.forbidden]) {
      expect(challengeEnded(new ApiError("http", { method: "POST", status }))).toBe(true);
    }
  });

  it("keeps the challenge for a rejected code or an unknown outcome", () => {
    for (const err of [
      new ApiError("http", { method: "POST", status: httpStatus.unprocessable }),
      new ApiError("http", { method: "POST", status: httpStatus.internalServerError }),
      new ApiError("network", { method: "POST" }),
    ]) {
      expect(challengeEnded(err)).toBe(false);
    }
  });
});
