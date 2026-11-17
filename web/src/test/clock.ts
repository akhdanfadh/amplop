import { vi } from "vite-plus/test";

// Clock moves the running test's fake clock. advance moves it by ms,
// running every timer that comes due on the way, in order, and returns
// once the page has drawn what they changed: a plain `expect` may follow
// it. A read a timer began has reached the fake server by then, but its
// answer may land a moment later, so what the answer shows is checked
// with a retrying assertion.
export type Clock = { advance: (ms: number) => Promise<void> };

// fakeClock makes the page's timers and its date fake for the rest of the
// running test, in a browser test or a unit test, and returns the Clock
// that moves them. Time then stands still until the test moves it, so a
// test waits out none of the app's timers: it moves the clock by the
// app's own constant, e.g. `await clock.advance(sendPollMs)`. The date
// starts at the real one; `vi.setSystemTime` sets another. The setup
// puts the real clock back after each test (setup.ts).
//
// DESIGN: it fakes setTimeout, setInterval, their clear functions, and
// Date, together, for every caller: code that sets a timer often reads
// the date too, e.g. React Aria's toast works out the time left from
// Date, and TanStack Query's staleTime compares dates while its
// refetchInterval is a setInterval and its retry delay a setTimeout. It
// leaves the frame clock (requestAnimationFrame), performance.now, and
// microtasks real: React, React Aria's virtualized lists and transitions
// draw on those, and under a plain `vi.useFakeTimers()` a frame never
// comes until the test moves the clock.
//
// What a test author must know, each checked on Vitest 5.1 by a test
// that read Date.now() around the call, and in @vitest/browser's
// context.js and locators, and vitest's expect.poll and vi.waitFor:
//
//   - Call it before the test renders: a timer the app set on the real
//     clock stays real.
//   - Vitest's own waits run on the real clock, so a retrying assertion
//     still retries and a hanging test still times out.
//   - `expect.element` and `expect.poll` leave the clock alone when they
//     pass at the first look. Each failed look moves it: `expect.poll`
//     by its interval, 50 ms; `expect.element`, while its element is
//     missing, by 0, 20, 50, 100, 100, then 500 ms a look. So fake time
//     runs about as fast as real time while an assertion waits, and can
//     overshoot by up to 500 ms: an element due after 300 ms was found
//     with the clock 770 ms on.
//   - `vi.waitFor` moves the clock 50 ms before every look, the first
//     too.
//   - An action on an element (`click`, `fill`, `hover`, on a locator or
//     through `userEvent`) moves the clock 50 ms for each 50 ms of real
//     time it takes: usually 0, sometimes 50 or more on a busy machine.
//     `userEvent.keyboard`, `userEvent.tab`, `page.viewport`, and
//     `locator.elements()` never move it.
//   - So a check that something has NOT happened yet holds only with a
//     margin wider than those steps, or with nothing between the clock's
//     moves but calls that never move it. pointTo (pointer.ts) hovers
//     without a round trip for that.
//   - TanStack Query hands each result to its components through
//     `setTimeout(0)`: under a fake clock a read's answer shows only once
//     the clock moves, by any amount, 0 included. A retrying assertion
//     does that by itself; a plain `expect` needs `await
//     clock.advance(0)` first.
//   - A poll that comes due while its read is still running joins that
//     read (TanStack Query starts no second one). Between two polls,
//     wait for the first one's answer.
//   - advance gives the page a turn after each timer it runs, so a long
//     move across a short interval is slow: 16 s across a 100 ms poll
//     took 0.6 s of real time.
//   - The transport gives up on a request after 15 s of this clock
//     (transport.ts), so an answer a test holds across a longer advance
//     fails as a timeout. A fake server's handler that waits with
//     setTimeout waits on this clock too.
export function fakeClock(): Clock {
  vi.useFakeTimers({
    toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"],
  });
  return {
    advance: async (ms) => {
      await vi.advanceTimersByTimeAsync(ms);
      // React can draw what a timer changed a frame later. A unit test
      // has no page.
      if (typeof requestAnimationFrame === "function")
        await new Promise((resolve) => requestAnimationFrame(resolve));
    },
  };
}
