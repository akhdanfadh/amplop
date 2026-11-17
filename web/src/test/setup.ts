import { afterEach, vi } from "vite-plus/test";
import { expectAllAnswered } from "./server";

// Setup for every test, unit and browser: a test that faked the clock
// leaves the real one to the next, and a test whose fake server got a
// request no route knew fails.
afterEach(() => {
  vi.useRealTimers();
  expectAllAnswered();
});
