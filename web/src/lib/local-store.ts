// localStore is this browser's localStorage for one value replaced whole,
// e.g. the sidebar's collapsed state; web/README.md, "Kept in the
// browser", says what may be kept and which way. A browser can refuse
// storage (a private window, a full quota, a blocked site); then a read
// finds nothing and a write is dropped, so the layout starts from its
// defaults.
export const localStore = {
  getItem(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  setItem(key: string, value: string): void {
    try {
      localStorage.setItem(key, value);
    } catch {
      // Dropped: see above.
    }
  },
  removeItem(key: string): void {
    try {
      localStorage.removeItem(key);
    } catch {
      // Dropped: see above.
    }
  },
};
