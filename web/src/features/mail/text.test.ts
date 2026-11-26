import { describe, expect, it } from "vite-plus/test";
import type { ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  checkFailures,
  checkRefusal,
  pickedNumber,
  refusalSentences,
  subjectText,
  syncingDetail,
} from "./text";
import { actionWords } from "./words";

// refused returns an error the API sent with status and type.
function refused(status: number, type: ProblemType): ApiError {
  return new ApiError("http", {
    method: "POST",
    status,
    problem: { type, status, title: "Refused", detail: "Refused." },
  });
}

describe("checkRefusal", () => {
  it("counts a check started moments ago as started", () => {
    expect(checkRefusal(refused(429, "/api/v0/problems/sync-throttled"))).toBeUndefined();
  });

  it("tells a gone account, one to reconnect, and an unknown outcome apart", () => {
    expect(checkRefusal(refused(404, "/api/v0/problems/connected-account-not-found"))).toEqual({
      kind: "gone",
    });
    expect(checkRefusal(refused(409, "/api/v0/problems/account-needs-reconnect"))).toEqual({
      kind: "reconnect",
    });
    expect(checkRefusal(new ApiError("network", { method: "POST" }))).toEqual({
      kind: "uncertain",
    });
  });

  it("tells an instance without Gmail from an account to reconnect", () => {
    expect(checkRefusal(refused(409, "/api/v0/problems/gmail-not-configured"))).toEqual({
      kind: "unavailable",
    });
    expect(
      checkFailures([
        { name: "Work", refusal: { kind: "unavailable" } },
        { name: "Home", refusal: { kind: "unavailable" } },
      ]),
    ).toBe("amplop can't check 2 accounts until an administrator sets up Gmail: Work and Home.");
  });
});

describe("checkFailures", () => {
  it("says one account's refusal in a sentence of its own", () => {
    expect(checkFailures([{ name: "Work", refusal: { kind: "reconnect" } }])).toBe(
      "Reconnect Work in Connected accounts to get its mail.",
    );
  });

  it("names every account a reason holds in one sentence", () => {
    expect(
      checkFailures([
        { name: "Rodzina", refusal: { kind: "gone" } },
        { name: "Work", refusal: { kind: "reconnect" } },
        { name: "a@b.co", refusal: { kind: "gone" } },
        { name: "Ninth", refusal: { kind: "gone" } },
      ]),
    ).toBe(
      "3 accounts are no longer connected: Rodzina, a@b.co, and Ninth. Reconnect Work in Connected accounts to get its mail.",
    );
    expect(
      checkFailures([
        { name: "Work", refusal: { kind: "uncertain" } },
        { name: "Home", refusal: { kind: "uncertain" } },
      ]),
    ).toBe(
      "Couldn't tell whether amplop checked 2 accounts: Work and Home. Their mail still arrives within a minute.",
    );
  });
});

describe("syncingDetail", () => {
  it("says how many messages a sync has copied", () => {
    expect(syncingDetail(1)).toBe("Copying the last 90 days: 1 message so far.");
    expect(syncingDetail(1200)).toBe("Copying the last 90 days: 1,200 messages so far.");
  });
});

describe("subjectText", () => {
  it("says when a message has no subject", () => {
    expect(subjectText("Lunch?")).toBe("Lunch?");
    expect(subjectText("  ")).toBe("(no subject)");
  });
});

describe("refusalSentences", () => {
  it("says one sentence per reason, naming each account to reconnect", () => {
    expect(
      refusalSentences({
        reconnect: ["Work", "Home"],
        gone: 2,
        failed: 1,
        errors: ["amplop isn't answering.", "amplop isn't answering."],
      }),
    ).toBe(
      "Reconnect 2 accounts in Connected accounts, then try again: Work and Home. " +
        "2 conversations are no longer in Gmail. " +
        "Gmail didn't answer for 1 conversation. Try again. " +
        "amplop isn't answering.",
    );
  });

  it("names one account to reconnect in its sentence", () => {
    expect(refusalSentences({ reconnect: ["Work"], gone: 1, failed: 0, errors: [] })).toBe(
      "Reconnect Work in Connected accounts, then try again. 1 conversation is no longer in Gmail.",
    );
  });
});

describe("pickedNumber", () => {
  it("says how many are picked, and All once every one of a place is", () => {
    expect(pickedNumber(25, false)).toBe("25");
    expect(pickedNumber(1284, true)).toBe("All 1,284");
  });
});

describe("actionWords.failed", () => {
  it("names the conversation acted on, or how many of several failed", () => {
    expect(actionWords.archive.failed()).toBe("Couldn't archive the conversation");
    expect(actionWords.archive.failed(1)).toBe("Couldn't archive 1 conversation");
    expect(actionWords.unstar.failed()).toBe("Couldn't remove the star");
    expect(actionWords.unstar.failed(3)).toBe("Couldn't remove the stars from 3 conversations");
  });
});
