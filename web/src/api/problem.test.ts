import { describe, expect, it } from "vite-plus/test";
import {
  formErrors,
  formSummary,
  problemExplanation,
  uncertainMessage,
  uncertainOutcome,
  userMessage,
} from "./problem";
import type { ProblemType } from "./generated/model";
import { ApiError } from "./transport";

const httpStatus = {
  created: 201,
  conflict: 409,
  unprocessable: 422,
  internalServerError: 500,
  badGateway: 502,
  unavailable: 503,
  gatewayTimeout: 504,
};

const serviceBusyType: ProblemType = "/api/v0/problems/service-busy";

describe("uncertainOutcome", () => {
  it.each([
    ["no response", new ApiError("network", { method: "POST" })],
    ["no response and no known method", new ApiError("network")],
    ["a timeout before any status", new ApiError("timeout", { method: "PUT" })],
    ["a 5xx", new ApiError("http", { method: "DELETE", status: httpStatus.unavailable })],
    [
      "an unreadable success",
      new ApiError("response", { method: "POST", status: httpStatus.created }),
    ],
    [
      "a success cut off mid-body",
      new ApiError("network", { method: "PATCH", status: httpStatus.created }),
    ],
  ])("is true for a write with %s", (_, err) => {
    expect(uncertainOutcome(err)).toBe(true);
  });

  it.each([
    ["a received rejection", new ApiError("http", { method: "POST", status: httpStatus.conflict })],
    [
      "a rejection whose body timed out",
      new ApiError("timeout", { method: "POST", status: httpStatus.unprocessable }),
    ],
    ["a read with no response", new ApiError("network", { method: "GET" })],
    [
      "a read's 5xx",
      new ApiError("http", { method: "GET", status: httpStatus.internalServerError }),
    ],
  ])("is false for %s", (_, err) => {
    expect(uncertainOutcome(err)).toBe(false);
  });
});

describe("problemExplanation", () => {
  it("prefers the detail, trimmed", () => {
    const err = new ApiError("http", {
      problem: { title: "Conflict", detail: " The counter is full. " },
    });

    expect(problemExplanation(err)).toBe("The counter is full.");
  });

  it("falls back to the title", () => {
    const err = new ApiError("http", { problem: { title: "Conflict", detail: "  " } });

    expect(problemExplanation(err)).toBe("Conflict");
  });

  it("is undefined without a problem or any text in it", () => {
    expect(problemExplanation(new ApiError("network"))).toBeUndefined();
    expect(problemExplanation(new ApiError("http", { problem: { title: " " } }))).toBeUndefined();
  });
});

describe("uncertainMessage", () => {
  it("puts the explanation before the advice", () => {
    const err = new ApiError("http", { problem: { detail: "The service is restarting." } });

    expect(uncertainMessage(err, "Check before trying again.")).toBe(
      "The service is restarting. Check before trying again.",
    );
  });

  it("gives only the advice without an explanation", () => {
    expect(uncertainMessage(new ApiError("network"), "Check before trying again.")).toBe(
      "Check before trying again.",
    );
  });
});

describe("userMessage", () => {
  it("explains a read timeout without calling it an unreachable server", () => {
    expect(userMessage(new ApiError("timeout", { method: "GET" }))).toBe(
      "The request took too long. Try again.",
    );
  });

  it("keeps the write warning when waiting for confirmation times out", () => {
    expect(userMessage(new ApiError("timeout", { method: "POST" }))).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("preserves reported write success after a body-read timeout", () => {
    const err = new ApiError("timeout", { method: "POST", status: httpStatus.created });

    expect(userMessage(err)).toBe(
      "The server reported success, but we couldn't load the result. Check before submitting again.",
    );
  });

  it("keeps a known rejection when reading its explanation times out", () => {
    const err = new ApiError("timeout", { method: "POST", status: httpStatus.unprocessable });

    expect(userMessage(err)).toBe("Check the values and try again.");
  });

  it.each(["POST", "PUT", "PATCH", "DELETE"])("warns when a %s outcome is unknown", (method) => {
    const err = new ApiError("network", { method });

    expect(userMessage(err)).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("distinguishes a reported write success from an unreadable result", () => {
    const err = new ApiError("response", { method: "POST", status: httpStatus.created });

    expect(userMessage(err)).toBe(
      "The server reported success, but we couldn't load the result. Check before submitting again.",
    );
  });

  it("keeps a successful write's status when its body is interrupted", () => {
    const err = new ApiError("network", { method: "POST", status: httpStatus.created });

    expect(userMessage(err)).toBe(
      "The server reported success, but we couldn't load the result. Check before submitting again.",
    );
  });

  it.each([
    httpStatus.internalServerError,
    httpStatus.badGateway,
    httpStatus.unavailable,
    httpStatus.gatewayTimeout,
  ])("does not assume a write rolled back after HTTP %s", (status) => {
    const err = new ApiError("http", { method: "POST", status });

    expect(userMessage(err)).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("preserves safe server text alongside the uncertain write warning", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: httpStatus.unavailable,
      problem: { detail: "The service is restarting." },
    });

    expect(userMessage(err)).toBe(
      "The service is restarting. We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it.each([
    ["a read", "GET", "The server is busy and could not finish the request in time."],
    [
      "a write",
      "POST",
      "The server is busy and could not finish the request in time. We couldn't confirm whether this was saved. Check before submitting again.",
    ],
  ])("words Go's service-busy 503 for %s", (_, method, message) => {
    // A write may have committed before its request ran out of time.
    const err = new ApiError("http", {
      method,
      status: httpStatus.unavailable,
      retryAfter: "10",
      problem: {
        type: serviceBusyType,
        title: "Service busy",
        detail: "The server is busy and could not finish the request in time.",
      },
    });

    expect(userMessage(err)).toBe(message);
  });

  it("keeps a known write rejection's explanation", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: httpStatus.conflict,
      problem: { detail: "The counter is full." },
    });

    expect(userMessage(err)).toBe("The counter is full.");
  });

  it("keeps a write rejection when its body is interrupted", () => {
    const err = new ApiError("network", { method: "POST", status: httpStatus.unprocessable });

    expect(userMessage(err)).toBe("Check the values and try again.");
  });

  it("prefers the safe detail over the title", () => {
    const err = new ApiError("http", {
      method: "GET",
      status: 404,
      problem: { title: "Not Found", detail: "route not found" },
    });

    expect(userMessage(err)).toBe("route not found");
  });

  it("shows the title alone without a detail", () => {
    const err = new ApiError("http", {
      method: "GET",
      status: 502,
      problem: { title: "Bad Gateway" },
    });

    expect(userMessage(err)).toBe("Bad Gateway");
  });

  it("uses local wording when no problem arrived", () => {
    expect(userMessage(new ApiError("network", { method: "GET" }))).toBe(
      "Can't reach the server. Try again.",
    );
  });

  it("uses local wording when the received explanation is blank", () => {
    const err = new ApiError("http", {
      method: "GET",
      status: 422,
      problem: { title: " ", detail: "\t" },
    });

    expect(userMessage(err)).toBe("Check the values and try again.");
  });

  it.each([
    [
      "no response",
      new ApiError("network", { method: "GET" }),
      "Can't reach the server. Try again.",
    ],
    [
      "the proxy's 502",
      new ApiError("http", { method: "GET", status: 502 }),
      "Can't reach the server. Try again.",
    ],
    [
      "rejected values",
      new ApiError("http", { method: "GET", status: 422 }),
      "Check the values and try again.",
    ],
    [
      "a server failure",
      new ApiError("http", { method: "GET", status: 500 }),
      "Something went wrong on our side. Try again.",
    ],
    [
      "a timeout behind a proxy",
      new ApiError("http", { method: "GET", status: 504 }),
      "Something went wrong on our side. Try again.",
    ],
    [
      "a contract mismatch",
      new ApiError("response", { method: "GET" }),
      "Something went wrong. Try again.",
    ],
    [
      "a conflict without an explanation",
      new ApiError("http", { method: "GET", status: 409 }),
      "Something went wrong. Try again.",
    ],
    [
      "a safe detail for an unfamiliar problem",
      new ApiError("http", {
        method: "GET",
        status: 409,
        problem: { type: "/api/v0/problems/other-conflict", detail: "Choose another name." },
      }),
      "Choose another name.",
    ],
    [
      "actual server status despite a different problem status",
      new ApiError("http", { method: "GET", status: 503, problem: { status: 422 } }),
      "Something went wrong on our side. Try again.",
    ],
  ])("words %s for the user", (_case, err, message) => {
    expect(userMessage(err)).toBe(message);
  });
});

// A 422 from Go with these errors[] entries.
function unprocessable(errors: { location: string; message: string }[]): ApiError {
  return new ApiError("http", { method: "GET", status: 422, problem: { errors } });
}

// clock words a local time on a day of November 2026 as the app does, in
// the test's locale.
function clock(day: number, hour: number, minute: number): string {
  return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(
    new Date(2026, 10, day, hour, minute),
  );
}

// weekdayOf words a day of November 2026 as a weekday, in the test's locale.
function weekdayOf(date: number): string {
  return new Intl.DateTimeFormat(undefined, { weekday: "long" }).format(new Date(2026, 10, date));
}

describe("throttled requests", () => {
  const tooManyRequests = 429;
  // 14:30:20 local time on Sunday 1 November 2026.
  const receivedAt = new Date(2026, 10, 1, 14, 30, 20).getTime();
  const throttled = (retryAfter?: string, detail?: string, problem = detail !== undefined) =>
    new ApiError("http", {
      method: "POST",
      status: tooManyRequests,
      retryAfter,
      receivedAt,
      problem: problem ? { detail } : undefined,
    });
  it.each([
    ["0", 14, 31],
    ["40", 14, 31],
    ["41", 14, 32],
    ["180", 14, 34],
    ["5400", 16, 1],
  ])("rounds a wait of %s seconds up to the minute", (retryAfter, hour, minute) => {
    expect(userMessage(throttled(retryAfter, "Too many failed attempts."))).toBe(
      `Too many failed attempts. Try again after ${clock(1, hour, minute)}.`,
    );
  });

  it("names the day when the time is on another day", () => {
    expect(userMessage(throttled("86400", "Too many failed code attempts."))).toBe(
      `Too many failed code attempts. Try again after ${clock(2, 14, 31)} on ${weekdayOf(2)}.`,
    );
  });

  it("crosses midnight to the next day's name", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: tooManyRequests,
      retryAfter: "0",
      receivedAt: new Date(2026, 10, 1, 23, 59, 30).getTime(),
      problem: { detail: "Too many failed attempts." },
    });
    expect(userMessage(err)).toBe(
      `Too many failed attempts. Try again after ${clock(2, 0, 0)} on ${weekdayOf(2)}.`,
    );
  });

  it("counts a past HTTP date from when the response arrived", () => {
    const header = new Date(2026, 9, 31, 14, 0, 0).toUTCString();
    expect(userMessage(throttled(header, "Too many failed attempts."))).toBe(
      `Too many failed attempts. Try again after ${clock(1, 14, 31)}.`,
    );
  });

  it.each(["518401", "99999999999999", "9".repeat(400)])(
    "says later for a wait over six days (%s seconds)",
    (retryAfter) => {
      expect(userMessage(throttled(retryAfter, "Too many failed attempts."))).toBe(
        "Too many failed attempts. Try again later.",
      );
    },
  );

  it("reads an HTTP date as the time itself", () => {
    const header = new Date(2026, 10, 1, 15, 0, 0).toUTCString();
    expect(userMessage(throttled(header, "Too many failed attempts."))).toBe(
      `Too many failed attempts. Try again after ${clock(1, 15, 0)}.`,
    );
  });

  it.each([undefined, "", "soon"])("says later without a usable wait (%s)", (retryAfter) => {
    expect(userMessage(throttled(retryAfter, "Too many failed attempts."))).toBe(
      "Too many failed attempts. Try again later.",
    );
  });

  it("says later when no response time was recorded", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: tooManyRequests,
      retryAfter: "180",
    });
    expect(userMessage(err)).toBe("Too many attempts. Try again later.");
  });

  it("uses local wording when no problem arrived", () => {
    expect(userMessage(throttled("180"))).toBe(
      `Too many attempts. Try again after ${clock(1, 14, 34)}.`,
    );
  });

  it("ends a title-only problem with a full stop", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: tooManyRequests,
      retryAfter: "180",
      receivedAt,
      problem: { title: "Too many code attempts" },
    });
    expect(userMessage(err)).toBe(`Too many code attempts. Try again after ${clock(1, 14, 34)}.`);
  });
});

describe("formErrors", () => {
  it("keeps safe messages on rendered fields and returns unmatched messages for the summary", () => {
    const err = unprocessable([
      { location: "body.note", message: "Use at most 120 characters." },
      { location: "body.amount", message: "Enter a whole number from 1 to 10." },
      { location: "body.owner", message: "Choose an owner." },
      { location: "query.page", message: "Enter a whole number." },
    ]);

    expect(formErrors(err, ["note", "amount"])).toEqual({
      fields: {
        note: [{ message: "Use at most 120 characters." }],
        amount: [{ message: "Enter a whole number from 1 to 10." }],
      },
      unmatched: [{ message: "Choose an owner." }, { message: "Enter a whole number." }],
    });
  });

  it("matches nested paths exactly and preserves several messages per field", () => {
    const err = unprocessable([
      { location: "body.items[3].tags", message: "Choose at least one tag." },
      { location: "body.items[3].tags", message: "Choose no more than four tags." },
      { location: "body.items[3].name", message: "This field is required." },
    ]);

    expect(formErrors(err, ["items[3].tags"])).toEqual({
      fields: {
        "items[3].tags": [
          { message: "Choose at least one tag." },
          { message: "Choose no more than four tags." },
        ],
      },
      unmatched: [{ message: "This field is required." }],
    });
  });

  it("keeps whole-body errors in the summary and gives blank messages a fallback", () => {
    const err = unprocessable([
      { location: "body", message: "Correct the submitted input." },
      { location: "body.note", message: " " },
    ]);

    expect(formErrors(err, ["note"])).toEqual({
      fields: { note: [{ message: "This value was not accepted." }] },
      unmatched: [{ message: "Correct the submitted input." }],
    });
  });

  it("maps explicit business input errors even on a 409", () => {
    const err = new ApiError("http", {
      status: 409,
      problem: { errors: [{ location: "body.note", message: "Choose another note." }] },
    });

    expect(formErrors(err, ["note"])).toEqual({
      fields: { note: [{ message: "Choose another note." }] },
      unmatched: [],
    });
  });

  it("has no input errors for a temporary request failure", () => {
    const err = new ApiError("http", {
      status: 503,
      problem: { detail: "The service is restarting." },
    });

    expect(formErrors(err, ["note"])).toEqual({ fields: {}, unmatched: [] });
  });
});

describe("formSummary", () => {
  const message = "Check the values and try again.";

  it("is empty when every input error shows on a field", () => {
    const err = unprocessable([
      { location: "body.note", message: "Use at most 120 characters." },
      { location: "body.amount", message: "Enter a whole number from 1 to 10." },
    ]);

    expect(formSummary(message, formErrors(err, ["note", "amount"]))).toEqual([]);
  });

  it("follows the message with the input errors no field shows", () => {
    const err = unprocessable([
      { location: "body.note", message: "Use at most 120 characters." },
      { location: "body", message: "Correct the submitted input." },
    ]);

    expect(formSummary(message, formErrors(err, ["note"]))).toEqual([
      { message },
      { message: "Correct the submitted input." },
    ]);
  });

  it("keeps the message when no input error matches a field", () => {
    const err = unprocessable([{ location: "body.owner", message: "Choose an owner." }]);

    expect(formSummary(message, formErrors(err, ["note"]))).toEqual([
      { message },
      { message: "Choose an owner." },
    ]);
  });

  it("is the message alone for a failure without input errors", () => {
    const err = new ApiError("http", {
      status: httpStatus.unavailable,
      problem: { detail: "The service is restarting." },
    });

    expect(formSummary("The service is restarting.", formErrors(err, ["note"]))).toEqual([
      { message: "The service is restarting." },
    ]);
  });
});
