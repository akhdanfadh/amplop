// Setup for every browser test: the app's stylesheet, so components lay
// out, hide, and show as they do in the app, a fetch that never reaches
// the real API, the same page size at the start of each test, and the
// cleanup after each test, so a test file adds none for what it rendered,
// its query clients, toasts, the connection, or storage.
import "../styles.css";
import { onlineManager } from "@tanstack/react-query";
import { afterEach, beforeEach } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup } from "vitest-browser-react";
import { closeToasts } from "@/components/ui/toast";
import { endQueryClients } from "./query-client";
import { defaultViewport } from "./viewport";

// The page's own fetch, which Vitest puts back before each test, sends
// nothing to the API: a request for it with no fake server installed
// fails as a lost connection does.
//
// NOTE: work a test left running can still read once its fake server is
// gone, e.g. a navigation whose loader, a chunk loaded on first use, had
// yet to run. Through Vite's dev proxy that read would reach whatever
// listens on the Go server's port, and a 401 from there would end the
// session on the next test's page.
const pageFetch = globalThis.fetch;
globalThis.fetch = (input, init) => {
  const url = new URL(input instanceof Request ? input.url : input, location.href);
  if (url.origin === location.origin && url.pathname.startsWith("/api/"))
    return Promise.reject(new TypeError("No fake server is installed."));
  return pageFetch(input, init);
};

// Each test starts at the default size, whatever the test before it set.
beforeEach(async () => {
  await page.viewport(defaultViewport.width, defaultViewport.height);
});

// NOTE: Vitest runs afterEach hooks in reverse order of registration
// (sequence.hooks is "stack" by default), and registers the setup files'
// hooks in setupFiles' order, before a test file's own. So after a test
// its file's afterEach runs first, then this one, then setup.ts's, which
// checks the fake server's unanswered requests and puts the real timers
// back: by then the page is gone and no read is running. vite.config.ts
// lists setup.ts before this file for that.
afterEach(async () => {
  // 1. Unmount what the test rendered, first: a mounted list would read
  // again as its member's data ends.
  await cleanup();
  // 2. End the member's data and cancel the reads still running, while
  // the fake server still stands in for fetch.
  await endQueryClients();
  // A toast of a test that made no query client.
  closeToasts();
  // 3. Back online only now: a client still holding a paused request
  // would send it.
  onlineManager.setOnline(true);
  // Last: ending the member's data writes the folds' storage.
  localStorage.clear();
  sessionStorage.clear();
});
