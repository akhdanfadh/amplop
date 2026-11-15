import { describe, expect, it } from "vite-plus/test";
import { IR } from "@tanstack/db";
import { parseScope } from "./conversations";

const ref = (name: string) => new IR.PropRef([name]);
const value = (v: unknown) => new IR.Value(v);
const call = (name: string, ...args: IR.BasicExpression[]) => new IR.Func(name, args);

describe("parseScope", () => {
  it("reads a view and one account", () => {
    const where = call(
      "and",
      call("eq", ref("inbox"), value(true)),
      call("eq", ref("accountId"), value(3)),
    );
    expect(parseScope({ where })).toEqual({ place: "inbox", account: 3 });
  });

  it("reads Starred, which is starred in All mail", () => {
    const where = call(
      "and",
      call("eq", ref("starred"), value(true)),
      call("eq", ref("allMail"), value(true)),
    );
    expect(parseScope({ where })).toMatchObject({ place: "starred" });
  });

  it("reads All mail alone as all", () => {
    expect(parseScope({ where: call("eq", ref("allMail"), value(true)) })).toMatchObject({
      place: "all",
    });
  });

  it("reads a label from a contains test on labelIds", () => {
    const where = call("in", value("label-id"), ref("labelIds"));
    expect(parseScope({ where })).toMatchObject({ place: "label-id" });
  });

  it("reads a page's cursor and a tie's date", () => {
    const where = call("eq", ref("inbox"), value(true));
    expect(
      parseScope({
        where,
        cursor: {
          whereFrom: call("lt", ref("dateMs"), value(1000)),
          whereCurrent: call("eq", ref("dateMs"), value(1000)),
        },
      }),
    ).toMatchObject({ upper: { dateMs: 1000, inclusive: false } });
    expect(
      parseScope({ where: call("and", where, call("eq", ref("dateMs"), value(1000))) }),
    ).toMatchObject({ dateMs: 1000 });
  });

  it("refuses what no list asks for", () => {
    expect(() => parseScope({ where: call("eq", ref("subject"), value("x")) })).toThrow(
      "Unexpected eq on subject.",
    );
    expect(() => parseScope({ where: call("eq", ref("accountId"), value(1)) })).toThrow(
      "No single place in a load.",
    );
    // Starred is always asked with All mail.
    expect(() => parseScope({ where: call("eq", ref("starred"), value(true)) })).toThrow(
      "No single place in a load.",
    );
  });
});
