import { createCn } from "cn/engine";
import tables from "./generated/cn-tables";

// cn joins class names and merges conflicting Tailwind classes, the last
// winning, e.g. a component's base classes and the className it is given.
//
// DESIGN: the stock `cn` package knows only Tailwind's own scales, so it
// takes the theme's text-snippet for a colour, dropping it beside
// text-muted-foreground, and keeps both of text-sm and text-snippet.
// generated/cn-tables.ts is compiled by `cn build` from src/styles.css's
// @theme (`pnpm generate`), so every scale the theme adds merges as its
// kind.
// Import cn from here; the lint rule refuses the package's own.
export const cn = createCn(tables);
