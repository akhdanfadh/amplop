import { describe, expect, it } from "vite-plus/test";
import type { ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import { checkFailures, checkRefusal, syncingDetail } from "./text";

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
      "Couldn't tell whether inbok checked 2 accounts: Work and Home. Their mail still arrives within a minute.",
    );
  });
});

describe("syncingDetail", () => {
  it("says how many messages a sync has copied", () => {
    expect(syncingDetail(1)).toBe("Copying the last 90 days: 1 message so far.");
    expect(syncingDetail(1200)).toBe("Copying the last 90 days: 1,200 messages so far.");
  });
});
