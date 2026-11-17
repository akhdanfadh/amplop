import { afterEach, vi } from "vite-plus/test";

// Setup for every test, unit and browser: a test that faked the clock
// leaves the real one to the next.
afterEach(() => {
  vi.useRealTimers();
});
