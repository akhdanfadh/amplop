import { describe, expect, it } from "vite-plus/test";
import { ReceivedProblem } from "./received-problem";

const conflictStatus = 409;

describe("ReceivedProblem", () => {
  it("accepts an external problem without inventing absent members", () => {
    expect(ReceivedProblem.parse({ detail: "Maintenance is in progress." })).toEqual({
      detail: "Maintenance is in progress.",
    });
    expect(ReceivedProblem.parse({})).toEqual({});
  });

  it("keeps public members and strips unknown fields and rejected values", () => {
    expect(
      ReceivedProblem.parse({
        type: "/api/v0/problems/name-conflict",
        status: conflictStatus,
        title: "Name conflict",
        detail: "Choose another name.",
        instance: "/requests/example",
        errors: [{ location: "body.name", message: "Already used.", value: "private input" }],
        diagnostic: "private cause",
      }),
    ).toEqual({
      type: "/api/v0/problems/name-conflict",
      status: conflictStatus,
      title: "Name conflict",
      detail: "Choose another name.",
      instance: "/requests/example",
      errors: [{ location: "body.name", message: "Already used." }],
    });
  });

  it("ignores malformed members without losing usable detail", () => {
    expect(
      ReceivedProblem.parse({
        type: false,
        status: "503",
        title: [],
        detail: "Try again later.",
        instance: null,
        errors: "private diagnostic",
      }),
    ).toEqual({ detail: "Try again later." });
  });

  it("ignores invalid detail while preserving the title", () => {
    expect(ReceivedProblem.parse({ title: "Unavailable", detail: {} })).toEqual({
      title: "Unavailable",
    });
  });

  it("ignores unsupported IP formats without losing usable detail", () => {
    expect(
      ReceivedProblem.parse({
        type: "https://[v1.a]/problem",
        instance: "https://[v1.a]/request",
        detail: "Try again later.",
      }),
    ).toEqual({ detail: "Try again later." });
  });

  it.each([99, 600, 409.5, null])("ignores invalid HTTP status %s", (status) => {
    expect(ReceivedProblem.parse({ status, detail: "Try again." })).toEqual({
      detail: "Try again.",
    });
  });

  it.each([
    { errors: null },
    { errors: [] },
    { errors: [null] },
    { errors: [{ location: "body.name" }] },
    { errors: [{ message: "Required." }] },
  ])("ignores an unusable input-error list: $errors", ({ errors }) => {
    expect(ReceivedProblem.parse({ errors, detail: "Check the values." })).toEqual({
      detail: "Check the values.",
    });
  });

  it.each([{ body: null }, { body: [] }, { body: "failure" }, { body: conflictStatus }])(
    "rejects a non-object body: $body",
    ({ body }) => {
      expect(ReceivedProblem.safeParse(body).success).toBe(false);
    },
  );

  it.each([
    "",
    "/api/v0/problems/counter-full",
    "../problems/taken",
    "?reason=taken",
    "#detail",
    "urn:example:error",
    "https://proxy.example/problems/maintenance",
    "https://[::1]/problems/taken",
    "/problems/%5Bprivate%5D",
  ])("preserves the valid URI reference %j", (reference) => {
    expect(ReceivedProblem.parse({ type: reference, instance: reference })).toEqual({
      type: reference,
      instance: reference,
    });
  });

  it.each([
    "bad reference",
    "/bad%escape",
    "1bad:scheme",
    "/problems/taken#one#two",
    "https://example.invalid/problems/[private]",
    "/problems/\\private",
    "/problems/\nprivate",
  ])("ignores the malformed URI reference %j without changing it", (reference) => {
    expect(
      ReceivedProblem.parse({ type: reference, instance: reference, detail: "Try again." }),
    ).toEqual({ detail: "Try again." });
  });
});
