import { createContext, useContext, useState, useCallback, useMemo, type ReactNode } from "react";
import { useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { Navigate, trimPathRight, useRouter, useRouterState } from "@tanstack/react-router";
import { useGetSetupStatus, getGetSetupStatusQueryKey } from "@/api/generated/inbok";
import type { ApiError } from "@/api/transport";
import type { ResponseSetupStatus } from "@/api/generated/model";
import { FailedRead, RoutePending } from "@/components/route-states";

// SetupState is the shared installation status and setup actions.
type SetupState = {
  status: UseQueryResult<ResponseSetupStatus, ApiError>;
  attempt: () => void;
  complete: () => Promise<void>;
  completedHere: boolean;
};
const SetupContext = createContext<SetupState | null>(null);

// useSetup reads installation state owned by the application gate.
export function useSetup(): SetupState {
  const state = useContext(SetupContext);
  if (!state) throw new Error("SetupGate is missing.");
  return state;
}

// SetupGate restricts all pages to setup until the backend reports completion.
export function SetupGate({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const router = useRouter();
  const isSetup = useRouterState({
    // The URL can change before the previous page has finished unmounting.
    select: (state) =>
      trimPathRight(state.location.pathname).toLowerCase() === "/setup" &&
      state.matches.at(-1)?.routeId === "/setup",
  });
  const status = useGetSetupStatus({
    // Keep setup reachable even when the browser reports offline.
    query: { staleTime: Infinity, networkMode: "always" },
  });
  // A later status check can confirm an attempt whose response was lost.
  const [attemptedHere, setAttemptedHere] = useState(false);
  const completedHere = attemptedHere && status.data?.data.status === "done";

  const attempt = useCallback(() => setAttemptedHere(true), []);
  const complete = useCallback(async (): Promise<void> => {
    // An older status response must not overwrite a successful setup claim.
    await client.cancelQueries({ queryKey: getGetSetupStatusQueryKey(), exact: true });
    setAttemptedHere(true);
    client.setQueryData(getGetSetupStatusQueryKey(), { data: { status: "done" } });
  }, [client]);
  const value = useMemo(
    () => ({ status, attempt, complete, completedHere }),
    [status, attempt, complete, completedHere],
  );

  // Failed rechecks keep the form mounted so entered values survive retry.
  if (!status.data) {
    if (status.error) {
      return (
        <FailedRead
          // A new failure is a new page, whose heading takes focus again,
          // also when it comes before the loading view had a frame.
          key={status.errorUpdateCount}
          error={status.error}
          onRetry={() => {
            void status.refetch();
            // The page's own checks may have failed behind this one too.
            void router.invalidate();
          }}
        />
      );
    }
    // The app's loading view, where the router's will be (the root's
    // column), so the page does not jump. After Try again it is a later
    // load's, shown at once, as the router's is after its Try again.
    return (
      <div className="flex min-h-svh flex-col">
        <RoutePending moment={status.errorUpdateCount > 0 ? "later" : "first-load"} />
      </div>
    );
  }

  const done = status.data.data.status === "done";
  if (!done && !isSetup) return <Navigate to="/setup" replace />;
  if (done && isSetup) return <Navigate to="/" replace />;

  return <SetupContext.Provider value={value}>{children}</SetupContext.Provider>;
}
