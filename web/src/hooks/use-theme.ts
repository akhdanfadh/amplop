import { useSyncExternalStore } from "react";
import { readTheme, storeTheme, watchTheme, type Theme } from "@/lib/theme";

// useTheme returns this browser's theme choice and a function that changes
// it. The page follows the choice at once, here and in other tabs.
export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(watchTheme, readTheme, () => "system" as const);
  return [theme, storeTheme];
}
